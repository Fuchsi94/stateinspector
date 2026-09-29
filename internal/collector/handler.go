package collector

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Fuchsi94/stateinspector/internal/diff"
	"github.com/Fuchsi94/stateinspector/internal/extract"
	"github.com/Fuchsi94/stateinspector/internal/normalize"
	"github.com/Fuchsi94/stateinspector/internal/store"
)

// Sink ist der Ausschnitt des Stores, den der Collector braucht.
type Sink interface {
	WriteChanges(ctx context.Context, changes []store.Change) error
	Current(ctx context.Context, uid string) (store.Snapshot, bool, error)
}

// HandlerOptions konfiguriert den Event-Handler.
type HandlerOptions struct {
	// Context ist die Lebensdauer des Informers. Die Callback-Signatur von
	// client-go kennt keinen Context, deshalb haelt ihn der Handler.
	Context context.Context
	Cluster string
	// Watches entscheidet je Namespace. Der Cache kennt keine Ausschlussliste,
	// also filtert der Handler, wenn clusterweit beobachtet wird.
	Watches func(namespace string) bool
	Sink    Sink
	Out     chan<- store.Change
	// Now ist injizierbar, damit Tests feste Zeitpunkte setzen koennen.
	Now func() time.Time
}

// Handler uebersetzt Informer-Events in Aenderungen. Er blockiert nie auf der
// Datenbank: der teure Teil laeuft im Writer (R5).
type Handler struct {
	opts   HandlerOptions
	mu     sync.Mutex
	hashes map[string]string
}

var _ toolscache.ResourceEventHandler = (*Handler)(nil)

// NewHandler baut den Event-Handler.
func NewHandler(opts HandlerOptions) *Handler {
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Handler{opts: opts, hashes: map[string]string{}}
}

// OnAdd behandelt sowohl echte Neuanlagen als auch den initialen List nach
// einem Start. Welches davon es war, entscheidet nicht der Event-Typ, sondern
// ob die UID schon bekannt ist.
func (h *Handler) OnAdd(obj any, isInInitialList bool) {
	h.observe(obj, isInInitialList)
}

// OnUpdate behandelt Aenderungen am Objekt.
func (h *Handler) OnUpdate(_, newObj any) {
	h.observe(newObj, false)
}

// OnDelete behandelt Loeschungen, auch wenn nur ein Tombstone vorliegt.
func (h *Handler) OnDelete(obj any) {
	if tombstone, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
		// Der Watch hat das eigentliche Delete verpasst; der letzte bekannte
		// Zustand steckt im Tombstone.
		obj = tombstone.Obj
	}
	u, ok := obj.(*unstructured.Unstructured)
	if !ok || !h.relevant(u) {
		return
	}

	logger := h.logger(u)
	normalized, hash, err := normalizeAndHash(u)
	if err != nil {
		logger.Error(err, "Objekt beim Loeschen nicht normalisierbar")
		return
	}

	h.forget(string(u.GetUID()))
	h.emit(store.Change{
		UID: string(u.GetUID()), Cluster: h.opts.Cluster,
		APIGroup: u.GroupVersionKind().Group, APIVersion: u.GetAPIVersion(),
		Kind: u.GetKind(), Namespace: u.GetNamespace(), Name: u.GetName(),
		Type: store.Deleted, ObservedAt: h.opts.Now(), Hash: hash,
		Object: normalized, Images: extract.Images(normalized),
		Owner: extract.Owner(normalized),
	})
}

// observe ist der gemeinsame Weg fuer Add und Update.
func (h *Handler) observe(obj any, fromInitialList bool) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok || !h.relevant(u) {
		return
	}
	logger := h.logger(u)

	normalized, hash, err := normalizeAndHash(u)
	if err != nil {
		logger.Error(err, "Objekt nicht normalisierbar")
		return
	}

	uid := string(u.GetUID())
	if known, ok := h.knownHash(uid); ok && known == hash {
		return // R4: nichts hat sich geaendert.
	}

	previous, existed, err := h.opts.Sink.Current(h.opts.Context, uid)
	if err != nil {
		logger.Error(err, "letzten Stand nicht ladbar")
		return
	}
	if existed && previous.Hash == hash {
		h.remember(uid, hash)
		return
	}

	change := store.Change{
		UID: uid, Cluster: h.opts.Cluster,
		APIGroup: u.GroupVersionKind().Group, APIVersion: u.GetAPIVersion(),
		Kind: u.GetKind(), Namespace: u.GetNamespace(), Name: u.GetName(),
		ObservedAt: h.opts.Now(), Hash: hash, Object: normalized,
		Images: extract.Images(normalized), Owner: extract.Owner(normalized),
	}

	switch {
	case !existed:
		change.Type = store.Created
	default:
		change.Type = store.Updated
		// Eine Aenderung, die wir beim initialen List entdecken, ist waehrend
		// eines Ausfalls passiert (R13).
		change.Offline = fromInitialList
		result, err := diff.Between(previous.Object, normalized)
		if err != nil {
			logger.Error(err, "Diff nicht berechenbar")
		} else {
			change.Patch = result.Patch
			change.ChangedPaths = result.ChangedPaths
		}
	}

	h.remember(uid, hash)
	h.emit(change)
}

func (h *Handler) relevant(u *unstructured.Unstructured) bool {
	if u == nil || u.GetUID() == "" {
		return false
	}
	return h.opts.Watches == nil || h.opts.Watches(u.GetNamespace())
}

func (h *Handler) logger(u *unstructured.Unstructured) logr.Logger {
	return log.FromContext(h.opts.Context).WithValues(
		"gvk", u.GroupVersionKind().String(),
		"namespace", u.GetNamespace(),
		"name", u.GetName(),
		"uid", string(u.GetUID()),
	)
}

// emit blockiert bewusst, wenn der Puffer voll ist. Eine Luecke in der
// Historie waere schaedlicher als Rueckstau (R5, KTD6). Der erste Versuch
// dient nur dazu, den Rueckstau sichtbar zu machen, bevor wir warten.
func (h *Handler) emit(c store.Change) {
	select {
	case h.opts.Out <- c:
		return
	default:
		bufferFull.Inc()
	}
	select {
	case h.opts.Out <- c:
	case <-h.opts.Context.Done():
	}
}

func (h *Handler) knownHash(uid string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	hash, ok := h.hashes[uid]
	return hash, ok
}

func (h *Handler) remember(uid, hash string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hashes[uid] = hash
}

func (h *Handler) forget(uid string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.hashes, uid)
}

func normalizeAndHash(u *unstructured.Unstructured) (map[string]any, string, error) {
	normalized, err := normalize.Object(u)
	if err != nil {
		return nil, "", fmt.Errorf("normalize object: %w", err)
	}
	hash, err := normalize.Hash(normalized)
	if err != nil {
		return nil, "", fmt.Errorf("hash object: %w", err)
	}
	return normalized, hash, nil
}
