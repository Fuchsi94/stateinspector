//go:build envtest

package collector_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Fuchsi94/stateinspector/internal/collector"
	"github.com/Fuchsi94/stateinspector/internal/store"
)

var (
	restConfig  *rest.Config
	databaseURL string
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	env := &envtest.Environment{}
	cfg, err := env.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "envtest starten: %v\n", err)
		os.Exit(1)
	}
	restConfig = cfg

	container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("stateinspector"),
		tcpostgres.WithUsername("stateinspector"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(2*time.Minute)),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Postgres starten: %v\n", err)
		_ = env.Stop()
		os.Exit(1)
	}
	databaseURL, err = container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Connection-String: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	_ = testcontainers.TerminateContainer(container)
	_ = env.Stop()
	os.Exit(code)
}

type harness struct {
	t      *testing.T
	client client.Client
	store  *store.Store
	ns     string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("Scheme: %v", err)
	}
	c, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("Client: %v", err)
	}

	db, err := store.New(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("store.New(): %v", err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate(): %v", err)
	}

	pool, err := pgxpool.New(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("Pruefverbindung: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(t.Context(), "TRUNCATE changes, resources"); err != nil {
		t.Fatalf("Tabellen leeren: %v", err)
	}

	// Jeder Test bekommt einen eigenen Namespace; envtest raeumt Namespaces
	// nicht ab, also duerfen sie sich nicht ins Gehege kommen.
	ns := fmt.Sprintf("t-%d", time.Now().UnixNano())
	if err := c.Create(t.Context(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}); err != nil {
		t.Fatalf("Namespace anlegen: %v", err)
	}
	return &harness{t: t, client: c, store: db, ns: ns}
}

// runCollector startet den Collector und liefert die Abbruchfunktion.
func (h *harness) runCollector() context.CancelFunc {
	h.t.Helper()
	ctx, cancel := context.WithCancel(h.t.Context())

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		h.t.Fatalf("Scheme: %v", err)
	}
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		h.t.Fatalf("Manager: %v", err)
	}
	if err := mgr.Add(collector.NewRunner(collector.RunnerOptions{
		Cluster:    "local",
		Cache:      mgr.GetCache(),
		Backend:    h.store,
		Watches:    func(ns string) bool { return ns == h.ns },
		BatchSize:  1,
		FlushEvery: 100 * time.Millisecond,
	})); err != nil {
		h.t.Fatalf("Runner: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = mgr.Start(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}

// changes wartet, bis die erwartete Anzahl Eintraege da ist, und gibt sie
// zurueck. Ohne Warten waere jeder Test ein Flake.
func (h *harness) changes(want int) []store.ChangeSummary {
	h.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last []store.ChangeSummary
	for time.Now().Before(deadline) {
		got, err := h.store.ListChanges(h.t.Context(), store.ChangeFilter{
			Cluster: "local", Namespace: h.ns,
			Since: time.Now().Add(-time.Hour), Until: time.Now().Add(time.Hour),
			Limit: 100,
		})
		if err != nil {
			h.t.Fatalf("ListChanges(): %v", err)
		}
		last = got
		if len(got) >= want {
			// Kurz nachfassen, damit ein unerwarteter Zusatzeintrag auffaellt.
			time.Sleep(500 * time.Millisecond)
			got, _ = h.store.ListChanges(h.t.Context(), store.ChangeFilter{
				Cluster: "local", Namespace: h.ns,
				Since: time.Now().Add(-time.Hour), Until: time.Now().Add(time.Hour),
				Limit: 100,
			})
			return got
		}
		time.Sleep(200 * time.Millisecond)
	}
	h.t.Fatalf("nur %d von %d erwarteten Aenderungen: %+v", len(last), want, last)
	return nil
}

func deployment(ns, name, image string) *appsv1.Deployment {
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{
					{Name: "nginx", Image: image},
				}},
			},
		},
	}
}

// Der Kernpfad: anlegen, aendern, nur Status patchen, loeschen.
func TestDeploymentLifecycleProducesExactlyThreeChanges(t *testing.T) {
	h := newHarness(t)
	stop := h.runCollector()
	defer stop()

	obj := deployment(h.ns, "web", "nginx:1.27")
	if err := h.client.Create(t.Context(), obj); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	h.changes(1)

	obj.Spec.Template.Spec.Containers[0].Image = "nginx:1.27-alpine"
	if err := h.client.Update(t.Context(), obj); err != nil {
		t.Fatalf("Update(): %v", err)
	}
	got := h.changes(2)

	var updated *store.ChangeSummary
	for i := range got {
		if got[i].Type == store.Updated {
			updated = &got[i]
		}
	}
	if updated == nil {
		t.Fatalf("kein updated-Eintrag: %+v", got)
	}
	wantPath := "/spec/template/spec/containers/0/image"
	if len(updated.ChangedPaths) != 1 || updated.ChangedPaths[0] != wantPath {
		t.Errorf("changed_paths = %v, want [%s]", updated.ChangedPaths, wantPath)
	}

	// R6: ein reines Status-Update darf nichts erzeugen.
	obj.Status.Replicas = 1
	obj.Status.ObservedGeneration = 2
	if err := h.client.Status().Update(t.Context(), obj); err != nil {
		t.Fatalf("Status().Update(): %v", err)
	}
	time.Sleep(2 * time.Second)
	if after := h.changes(2); len(after) != 2 {
		t.Errorf("Status-Update erzeugte einen Eintrag: %+v", after)
	}

	if err := h.client.Delete(t.Context(), obj); err != nil {
		t.Fatalf("Delete(): %v", err)
	}
	final := h.changes(3)
	if final[0].Type != store.Deleted {
		t.Errorf("neuester Eintrag ist %q, want deleted", final[0].Type)
	}
}

// R13: was waehrend des Ausfalls passiert, wird als offline nachgetragen.
func TestChangeWhileCollectorDownIsMarkedOffline(t *testing.T) {
	h := newHarness(t)

	stop := h.runCollector()
	obj := deployment(h.ns, "api", "nginx:1.25")
	if err := h.client.Create(t.Context(), obj); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	h.changes(1)
	stop()

	// Collector steht - jetzt aendern.
	if err := h.client.Get(t.Context(), client.ObjectKeyFromObject(obj), obj); err != nil {
		t.Fatalf("Get(): %v", err)
	}
	obj.Spec.Template.Spec.Containers[0].Image = "nginx:1.27"
	if err := h.client.Update(t.Context(), obj); err != nil {
		t.Fatalf("Update(): %v", err)
	}

	stop2 := h.runCollector()
	defer stop2()

	got := h.changes(2)
	if len(got) != 2 {
		t.Fatalf("%d Aenderungen, want genau 2: %+v", len(got), got)
	}
	if got[0].Type != store.Updated {
		t.Fatalf("neuester Eintrag ist %q, want updated", got[0].Type)
	}
	if !got[0].Offline {
		t.Error("offline = false; die Aenderung passierte waehrend des Ausfalls")
	}
}

// R8/R9: ConfigMap-Werte tauchen nirgends auf.
func TestConfigMapValuesNeverReachTheDatabase(t *testing.T) {
	h := newHarness(t)
	stop := h.runCollector()
	defer stop()

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app-config", Namespace: h.ns},
		Data:       map[string]string{"LOG_LEVEL": "geheim-info"},
	}
	if err := h.client.Create(t.Context(), cm); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	h.changes(1)

	cm.Data["LOG_LEVEL"] = "geheim-debug"
	if err := h.client.Update(t.Context(), cm); err != nil {
		t.Fatalf("Update(): %v", err)
	}
	got := h.changes(2)
	if len(got) != 2 {
		t.Errorf("%d Aenderungen, want 2: %+v", len(got), got)
	}

	pool, err := pgxpool.New(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("Pruefverbindung: %v", err)
	}
	defer pool.Close()
	for _, table := range []string{"changes", "resources"} {
		var hits int64
		query := fmt.Sprintf("SELECT count(*) FROM %s WHERE object::text LIKE '%%geheim-%%'", table)
		if err := pool.QueryRow(t.Context(), query).Scan(&hits); err != nil {
			t.Fatalf("Query: %v", err)
		}
		if hits != 0 {
			t.Errorf("%d Zeilen in %s enthalten einen ConfigMap-Wert", hits, table)
		}
	}
}

// Der API-Server setzt bei einem Service die clusterIP. Das ist kein Change.
func TestServiceDefaultsDoNotProduceASecondChange(t *testing.T) {
	h := newHarness(t)
	stop := h.runCollector()
	defer stop()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: h.ns},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "web"},
			Ports:    []corev1.ServicePort{{Port: 80}},
		},
	}
	if err := h.client.Create(t.Context(), svc); err != nil {
		t.Fatalf("Create(): %v", err)
	}

	got := h.changes(1)
	if len(got) != 1 {
		t.Errorf("%d Aenderungen, want genau 1: %+v", len(got), got)
	}
}
