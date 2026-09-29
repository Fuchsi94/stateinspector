package collector

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/Fuchsi94/stateinspector/internal/store"
)

// Backend buendelt, was der Collector vom Store braucht.
type Backend interface {
	Sink
	Lister
}

// RunnerOptions konfiguriert den Collector-Lauf.
type RunnerOptions struct {
	Cluster string
	Cache   cache.Cache
	Backend Backend
	// Watches filtert Namespaces, die der Cache nicht ausschliessen kann.
	Watches func(namespace string) bool
	// CacheSynced wird gemeldet, sobald Informer und Resync durch sind.
	CacheSynced func()
	BufferSize  int
	BatchSize   int
	FlushEvery  time.Duration
}

// Runner haengt die Event-Handler an den Cache, holt den Ausfall nach und
// betreibt den Writer. Er laeuft nur beim Leader (R3).
type Runner struct {
	opts RunnerOptions
}

var (
	_ manager.Runnable               = (*Runner)(nil)
	_ manager.LeaderElectionRunnable = (*Runner)(nil)
)

// NewRunner baut den Collector-Lauf.
func NewRunner(opts RunnerOptions) *Runner {
	if opts.BufferSize <= 0 {
		opts.BufferSize = 1024
	}
	return &Runner{opts: opts}
}

// NeedLeaderElection haelt mehrere Repliken davon ab, dieselbe Aenderung
// mehrfach zu schreiben.
func (r *Runner) NeedLeaderElection() bool { return true }

// Start registriert die Informer, wartet auf den Cache, holt verpasste
// Loeschungen nach und betreibt dann den Writer bis zum Ende des Contexts.
func (r *Runner) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("collector")

	changes := make(chan store.Change, r.opts.BufferSize)
	handler := NewHandler(HandlerOptions{
		Context: ctx,
		Cluster: r.opts.Cluster,
		Watches: r.opts.Watches,
		Sink:    r.opts.Backend,
		Out:     changes,
	})

	for _, gvk := range Watched {
		informer, err := r.opts.Cache.GetInformer(ctx, newUnstructured(gvk))
		if err != nil {
			return fmt.Errorf("get informer for %s: %w", gvk, err)
		}
		if _, err := informer.AddEventHandler(handler); err != nil {
			return fmt.Errorf("add event handler for %s: %w", gvk, err)
		}
		logger.Info("beobachte Ressourcenart", "gvk", gvk.String())
	}

	// Erst wenn jeder Informer gesynct ist, sagt das Fehlen eines Objekts im
	// Cache wirklich aus, dass es geloescht wurde.
	if !r.opts.Cache.WaitForCacheSync(ctx) {
		return fmt.Errorf("cache did not sync")
	}

	present, err := r.presentUIDs(ctx)
	if err != nil {
		return err
	}
	sweeper := NewSweeper(SweeperOptions{
		Cluster: r.opts.Cluster,
		Lister:  r.opts.Backend,
		Out:     changes,
	})
	if err := sweeper.Sweep(ctx, present); err != nil {
		return fmt.Errorf("resync: %w", err)
	}

	if r.opts.CacheSynced != nil {
		r.opts.CacheSynced()
	}

	writer := NewWriter(r.opts.Backend, changes, WriterOptions{
		BatchSize:     r.opts.BatchSize,
		FlushInterval: r.opts.FlushEvery,
	})
	return writer.Run(ctx)
}

// presentUIDs liest aus dem bereits gesyncten Cache, was es aktuell gibt.
func (r *Runner) presentUIDs(ctx context.Context) (map[string]struct{}, error) {
	present := map[string]struct{}{}
	for _, gvk := range Watched {
		list := &unstructured.UnstructuredList{}
		// Die Listen-GVK traegt das List-Suffix, sonst findet der Cache den
		// Informer nicht.
		list.SetGroupVersionKind(schema.GroupVersionKind{
			Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind + "List",
		})
		if err := r.opts.Cache.List(ctx, list); err != nil {
			return nil, fmt.Errorf("list %s from cache: %w", gvk, err)
		}
		for i := range list.Items {
			item := &list.Items[i]
			if r.opts.Watches != nil && !r.opts.Watches(item.GetNamespace()) {
				continue
			}
			present[string(item.GetUID())] = struct{}{}
		}
	}
	return present, nil
}

// newUnstructured baut ein leeres Objekt, das seine Art nur ueber die
// gesetzte GVK traegt - so kommt ein Handler fuer alle Arten aus (KTD1).
func newUnstructured(gvk schema.GroupVersionKind) client.Object {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	return u
}
