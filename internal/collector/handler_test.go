package collector_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	toolscache "k8s.io/client-go/tools/cache"

	"github.com/Fuchsi94/stateinspector/internal/collector"
	"github.com/Fuchsi94/stateinspector/internal/store"
)

// fakeSink ersetzt den Store. Er haelt fest, was geschrieben wurde, und kann
// Fehler simulieren.
type fakeSink struct {
	mu       sync.Mutex
	batches  [][]store.Change
	current  map[string]store.Snapshot
	failNext int
}

func newFakeSink() *fakeSink {
	return &fakeSink{current: map[string]store.Snapshot{}}
}

func (f *fakeSink) WriteChanges(_ context.Context, changes []store.Change) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext > 0 {
		f.failNext--
		return errors.New("simulierter Schreibfehler")
	}
	batch := make([]store.Change, len(changes))
	copy(batch, changes)
	f.batches = append(f.batches, batch)
	for _, c := range batch {
		f.current[c.UID] = store.Snapshot{Hash: c.Hash, Object: c.Object}
	}
	return nil
}

func (f *fakeSink) Current(_ context.Context, uid string) (store.Snapshot, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	snap, ok := f.current[uid]
	return snap, ok, nil
}

func (f *fakeSink) written() []store.Change {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.Change
	for _, b := range f.batches {
		out = append(out, b...)
	}
	return out
}

func (f *fakeSink) batchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.batches)
}

func deploymentObj(uid, image, resourceVersion string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name": "web", "namespace": "demo",
			"uid": uid, "resourceVersion": resourceVersion,
		},
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []any{map[string]any{"name": "nginx", "image": image}},
				},
			},
		},
	}}
}

func newHandler(t *testing.T, sink collector.Sink, out chan store.Change) *collector.Handler {
	t.Helper()
	return collector.NewHandler(collector.HandlerOptions{
		Context: t.Context(),
		Cluster: "local",
		Watches: func(string) bool { return true },
		Sink:    sink,
		Out:     out,
	})
}

func drain(t *testing.T, ch chan store.Change) []store.Change {
	t.Helper()
	var out []store.Change
	for {
		select {
		case c := <-ch:
			out = append(out, c)
		default:
			return out
		}
	}
}

// R4: derselbe Zustand zweimal gemeldet ergibt einen Eintrag.
func TestSameObjectTwiceYieldsOneChange(t *testing.T) {
	out := make(chan store.Change, 16)
	h := newHandler(t, newFakeSink(), out)
	obj := deploymentObj("11111111-1111-1111-1111-111111111111", "nginx:1.27", "1")

	h.OnAdd(obj, false)
	h.OnUpdate(obj, obj)

	got := drain(t, out)
	if len(got) != 1 {
		t.Fatalf("%d Aenderungen, want 1: %+v", len(got), got)
	}
	if got[0].Type != store.Created {
		t.Errorf("Type = %q, want created", got[0].Type)
	}
}

// R4/R6: bewegt sich nur Rauschen, entsteht kein Eintrag.
func TestNoiseOnlyChangeIsIgnored(t *testing.T) {
	out := make(chan store.Change, 16)
	h := newHandler(t, newFakeSink(), out)
	uid := "22222222-2222-2222-2222-222222222222"

	h.OnAdd(deploymentObj(uid, "nginx:1.27", "1"), false)
	h.OnUpdate(deploymentObj(uid, "nginx:1.27", "1"), deploymentObj(uid, "nginx:1.27", "9999"))

	if got := drain(t, out); len(got) != 1 {
		t.Errorf("%d Aenderungen, want 1 (nur das created)", len(got))
	}
}

// KTD9: der Event-Typ entscheidet nicht, die Existenz der UID entscheidet.
func TestKnownUIDProducesUpdateWithPatch(t *testing.T) {
	out := make(chan store.Change, 16)
	sink := newFakeSink()
	uid := "33333333-3333-3333-3333-333333333333"
	sink.current[uid] = store.Snapshot{
		Hash:   "veraltet",
		Object: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment"},
	}
	h := newHandler(t, sink, out)

	h.OnAdd(deploymentObj(uid, "nginx:1.27", "1"), false)

	got := drain(t, out)
	if len(got) != 1 {
		t.Fatalf("%d Aenderungen, want 1", len(got))
	}
	if got[0].Type != store.Updated {
		t.Errorf("Type = %q, want updated", got[0].Type)
	}
	if len(got[0].Patch) == 0 {
		t.Error("Patch ist leer, obwohl sich das Objekt geaendert hat")
	}
	if len(got[0].ChangedPaths) == 0 {
		t.Error("ChangedPaths ist leer")
	}
}

func TestUnknownUIDProducesCreated(t *testing.T) {
	out := make(chan store.Change, 16)
	h := newHandler(t, newFakeSink(), out)

	h.OnAdd(deploymentObj("44444444-4444-4444-4444-444444444444", "nginx:1.27", "1"), false)

	got := drain(t, out)
	if len(got) != 1 || got[0].Type != store.Created {
		t.Fatalf("want ein created, got %+v", got)
	}
	if len(got[0].Images) != 1 || got[0].Images[0] != "nginx:1.27" {
		t.Errorf("Images = %v, want [nginx:1.27]", got[0].Images)
	}
}

// Ein Tombstone ist der Normalfall nach einem verpassten Watch-Event.
func TestDeleteFromTombstoneIsRecovered(t *testing.T) {
	out := make(chan store.Change, 16)
	h := newHandler(t, newFakeSink(), out)
	uid := "55555555-5555-5555-5555-555555555555"
	obj := deploymentObj(uid, "nginx:1.27", "1")

	h.OnAdd(obj, false)
	drain(t, out)

	h.OnDelete(toolscache.DeletedFinalStateUnknown{Key: "demo/web", Obj: obj})

	got := drain(t, out)
	if len(got) != 1 {
		t.Fatalf("%d Aenderungen, want 1", len(got))
	}
	if got[0].Type != store.Deleted {
		t.Errorf("Type = %q, want deleted", got[0].Type)
	}
	if got[0].UID != uid {
		t.Errorf("UID = %q, want %q", got[0].UID, uid)
	}
	if len(got[0].Object) == 0 {
		t.Error("Object ist leer; der letzte Zustand ging verloren")
	}
}

// R2: ausgeschlossene Namespaces erzeugen gar nichts.
func TestExcludedNamespaceProducesNothing(t *testing.T) {
	out := make(chan store.Change, 16)
	h := collector.NewHandler(collector.HandlerOptions{
		Context: t.Context(),
		Cluster: "local",
		Watches: func(ns string) bool { return ns != "demo" },
		Sink:    newFakeSink(),
		Out:     out,
	})

	h.OnAdd(deploymentObj("66666666-6666-6666-6666-666666666666", "nginx:1.27", "1"), false)

	if got := drain(t, out); len(got) != 0 {
		t.Errorf("%d Aenderungen aus einem ausgeschlossenen Namespace", len(got))
	}
}

// R5: der Writer batcht und verliert nichts.
func TestWriterBatchesEverything(t *testing.T) {
	const n = 250
	sink := newFakeSink()
	in := make(chan store.Change, n)
	for i := 0; i < n; i++ {
		in <- store.Change{
			UID: fmt.Sprintf("77777777-7777-7777-7777-%012d", i),
			Type: store.Created, Cluster: "local", Hash: "h",
			ObservedAt: time.Now(), Object: map[string]any{"i": i},
		}
	}
	close(in)

	w := collector.NewWriter(sink, in, collector.WriterOptions{
		BatchSize: 100, FlushInterval: 50 * time.Millisecond,
	})
	if err := w.Run(t.Context()); err != nil {
		t.Fatalf("Run(): %v", err)
	}

	if got := len(sink.written()); got != n {
		t.Errorf("%d Aenderungen geschrieben, want %d", got, n)
	}
	if got := sink.batchCount(); got < 2 {
		t.Errorf("%d Batches, want mehr als einen bei %d Eintraegen", got, n)
	}
}

// Ein Schreibfehler darf den Writer nicht beenden - sonst steht die Historie still.
func TestWriterSurvivesStoreError(t *testing.T) {
	sink := newFakeSink()
	sink.failNext = 1
	in := make(chan store.Change, 4)
	in <- store.Change{UID: "88888888-8888-8888-8888-888888888881", Type: store.Created, Hash: "a", ObservedAt: time.Now()}
	in <- store.Change{UID: "88888888-8888-8888-8888-888888888882", Type: store.Created, Hash: "b", ObservedAt: time.Now()}
	close(in)

	w := collector.NewWriter(sink, in, collector.WriterOptions{
		BatchSize: 1, FlushInterval: 50 * time.Millisecond,
	})
	if err := w.Run(t.Context()); err != nil {
		t.Fatalf("Run() brach ab: %v", err)
	}

	// Der erste Batch scheiterte, der zweite muss durchgekommen sein.
	if got := len(sink.written()); got != 1 {
		t.Errorf("%d Aenderungen geschrieben, want 1 nach einem simulierten Fehler", got)
	}
}
