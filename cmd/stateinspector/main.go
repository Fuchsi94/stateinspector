// Command stateinspector beobachtet Kubernetes-Workloads read-only und
// historisiert jede echte Aenderung.
package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/Fuchsi94/stateinspector/internal/collector"
	"github.com/Fuchsi94/stateinspector/internal/mcp"
	"github.com/Fuchsi94/stateinspector/internal/store"
)

// defaultExcludeNamespaces sind die Namespaces, die ohne gegenteilige Angabe
// nicht beobachtet werden. Sie erzeugen nur Systemrauschen.
const defaultExcludeNamespaces = "kube-system,kube-public,kube-node-lease"

// leaderElectionID ist der Name der Lease, ueber die sich mehrere Repliken
// auf einen aktiven Collector einigen.
const leaderElectionID = "stateinspector.fuchsi94.github.io"

// options haelt die aufgeloesten Flags. Flags sind laut CLAUDE.md die einzige
// erlaubte Form globaler Konfiguration, und sie werden hier gebuendelt statt
// als Paketvariablen verstreut.
type options struct {
	clusterName       string
	leaderElect       bool
	metricsAddr       string
	healthProbeAddr   string
	mcpAddr           string
	namespaces        string
	excludeNamespaces string
}

func bindFlags(fs *flag.FlagSet) *options {
	var o options
	fs.StringVar(&o.clusterName, "cluster-name", "local",
		"Name des Clusters, wird an jede gespeicherte Zeile geschrieben")
	fs.BoolVar(&o.leaderElect, "leader-elect", false,
		"Leader Election aktivieren; nur der Leader schreibt Aenderungen")
	fs.StringVar(&o.metricsAddr, "metrics-bind-address", ":8080",
		"Adresse des controller-runtime Metrics-Endpunkts")
	fs.StringVar(&o.healthProbeAddr, "health-probe-bind-address", ":8082",
		"Adresse fuer /healthz und /readyz")
	fs.StringVar(&o.mcpAddr, "mcp-bind-address", "127.0.0.1:8081",
		"Adresse des MCP-Servers; nur loopback, Zugriff per Port-Forward")
	fs.StringVar(&o.namespaces, "namespaces", "",
		"Komma-Liste zu beobachtender Namespaces; leer bedeutet alle")
	fs.StringVar(&o.excludeNamespaces, "exclude-namespaces", defaultExcludeNamespaces,
		"Komma-Liste ausgeschlossener Namespaces")
	return &o
}

// splitList zerlegt eine Komma-Liste und wirft Leerraum und leere Eintraege weg,
// damit "a, ,b" und "a,b" dasselbe bedeuten.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// namespaceFilter entscheidet, welche Namespaces beobachtet werden.
type namespaceFilter struct {
	include map[string]struct{}
	exclude map[string]struct{}
}

func newNamespaceFilter(include, exclude []string) namespaceFilter {
	f := namespaceFilter{include: map[string]struct{}{}, exclude: map[string]struct{}{}}
	for _, ns := range include {
		f.include[ns] = struct{}{}
	}
	for _, ns := range exclude {
		f.exclude[ns] = struct{}{}
	}
	return f
}

// watches gilt fuer jedes Objekt. Ausschluss schlaegt Einschluss: sonst wuerde
// ein Tippfehler in --namespaces einen Systemnamespace oeffnen.
func (f namespaceFilter) watches(ns string) bool {
	if _, excluded := f.exclude[ns]; excluded {
		return false
	}
	if len(f.include) == 0 {
		return true
	}
	_, included := f.include[ns]
	return included
}

// cacheNamespaces schraenkt den Cache ein, wenn eine Include-Liste vorliegt.
// Ohne Include-Liste wird clusterweit beobachtet und erst der Event-Handler
// filtert, weil der Cache keine Ausschluss-Liste kennt.
func (f namespaceFilter) cacheNamespaces() map[string]cache.Config {
	if len(f.include) == 0 {
		return nil
	}
	out := map[string]cache.Config{}
	for ns := range f.include {
		if f.watches(ns) {
			out[ns] = cache.Config{}
		}
	}
	return out
}

// requireDatabaseURL liest die Pflichtvariable. getenv ist ein Parameter, damit
// der Fehlerfall ohne Prozessumgebung testbar bleibt.
func requireDatabaseURL(getenv func(string) string) (string, error) {
	url := strings.TrimSpace(getenv("DATABASE_URL"))
	if url == "" {
		return "", errors.New("DATABASE_URL is not set; the collector needs a Postgres connection")
	}
	return url, nil
}

// readyGate haelt die Vorbedingungen aus R21 fest. Readiness wird erst gruen,
// wenn Cache-Sync, Migrationen und die Datenbank stehen. Liveness haengt
// bewusst nicht daran, damit ein DB-Ausfall keine Restart-Schleife ausloest.
type readyGate struct {
	mu      sync.RWMutex
	pending map[string]struct{}
}

func newReadyGate(conditions ...string) *readyGate {
	g := &readyGate{pending: make(map[string]struct{}, len(conditions))}
	for _, c := range conditions {
		g.pending[c] = struct{}{}
	}
	return g
}

// done markiert eine Vorbedingung als erfuellt. Unbekannte Namen werden
// ignoriert, damit ein Tippfehler das Gate nicht vorzeitig oeffnet.
func (g *readyGate) done(condition string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.pending, condition)
}

func (g *readyGate) check(_ *http.Request) error {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if len(g.pending) == 0 {
		return nil
	}
	open := make([]string, 0, len(g.pending))
	for c := range g.pending {
		open = append(open, c)
	}
	sort.Strings(open)
	return fmt.Errorf("not ready: %s", strings.Join(open, ", "))
}

// cacheByObject haengt den ConfigMap-Transform vor den Informer-Store. Der
// Schluessel traegt die GVK, weil clusterweit mit unstructured beobachtet wird
// und der Go-Typ die Art damit nicht mehr verraet (KTD7).
func cacheByObject() map[client.Object]cache.ByObject {
	configMap := &unstructured.Unstructured{}
	configMap.SetGroupVersionKind(collector.ConfigMapGVK)
	return map[client.Object]cache.ByObject{
		configMap: {Transform: collector.StripConfigMapValues},
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "stateinspector: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	opts := bindFlags(fs)
	zapOpts := zap.Options{Development: false}
	zapOpts.BindFlags(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))
	log := ctrl.Log.WithName("setup")

	databaseURL, err := requireDatabaseURL(os.Getenv)
	if err != nil {
		return err
	}

	filter := newNamespaceFilter(splitList(opts.namespaces), splitList(opts.excludeNamespaces))

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return fmt.Errorf("build scheme: %w", err)
	}

	gate := newReadyGate("cache", "migrations", "database")

	ctx := ctrl.SetupSignalHandler()
	db, err := store.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect store: %w", err)
	}
	defer db.Close()

	// Readiness quittiert jede Vorbedingung einzeln, damit ein haengender
	// Schritt im Probe-Text sichtbar wird statt in einem pauschalen "not ready".
	if err := db.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate store: %w", err)
	}
	gate.done("migrations")
	if err := db.Ping(ctx); err != nil {
		return fmt.Errorf("reach store: %w", err)
	}
	gate.done("database")

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                  scheme,
		Metrics:                 metricsserver.Options{BindAddress: opts.metricsAddr},
		HealthProbeBindAddress:  opts.healthProbeAddr,
		LeaderElection:          opts.leaderElect,
		LeaderElectionID:        leaderElectionID,
		LeaderElectionNamespace: os.Getenv("POD_NAMESPACE"),
		Cache: cache.Options{
			DefaultNamespaces: filter.cacheNamespaces(),
			ByObject:          cacheByObject(),
		},
	})
	if err != nil {
		return fmt.Errorf("build manager: %w", err)
	}

	// Liveness beantwortet nur, ob der Prozess laeuft.
	if err := mgr.AddHealthzCheck("ping", healthz.Ping); err != nil {
		return fmt.Errorf("register healthz: %w", err)
	}
	if err := mgr.AddReadyzCheck("preconditions", gate.check); err != nil {
		return fmt.Errorf("register readyz: %w", err)
	}

	if err := mgr.Add(collector.NewRunner(collector.RunnerOptions{
		Cluster:     opts.clusterName,
		Cache:       mgr.GetCache(),
		Backend:     db,
		Watches:     filter.watches,
		CacheSynced: func() { gate.done("cache") },
	})); err != nil {
		return fmt.Errorf("add collector: %w", err)
	}

	// Lesen darf jede Replik; der Listener braucht deshalb keine Lease.
	if err := mgr.Add(mcp.NewListener(
		mcp.NewServer(mcp.Options{Backend: db, Cluster: opts.clusterName}),
		opts.mcpAddr,
	)); err != nil {
		return fmt.Errorf("add mcp listener: %w", err)
	}

	log.Info("starte stateinspector",
		"cluster", opts.clusterName,
		"leaderElect", opts.leaderElect,
		"metricsAddr", opts.metricsAddr,
		"healthProbeAddr", opts.healthProbeAddr,
		"mcpAddr", opts.mcpAddr,
		"namespaces", splitList(opts.namespaces),
		"excludeNamespaces", splitList(opts.excludeNamespaces),
	)

	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("run manager: %w", err)
	}
	return nil
}
