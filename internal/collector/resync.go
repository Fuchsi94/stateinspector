package collector

import (
	"context"
	"fmt"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Fuchsi94/stateinspector/internal/store"
)

// Lister ist der Ausschnitt des Stores, den der Sweep braucht.
type Lister interface {
	LiveResources(ctx context.Context, cluster string) ([]store.LiveResource, error)
}

// SweeperOptions konfiguriert den Abgleich nach dem Cache-Sync.
type SweeperOptions struct {
	Cluster string
	Lister  Lister
	Out     chan<- store.Change
	Now     func() time.Time
}

// Sweeper gleicht den gespeicherten Stand mit dem Cache ab. Ohne ihn bliebe
// alles, was waehrend eines Ausfalls geloescht wurde, fuer immer als lebendig
// verzeichnet (R13).
type Sweeper struct {
	opts SweeperOptions
}

// NewSweeper baut den Abgleich.
func NewSweeper(opts SweeperOptions) *Sweeper {
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Sweeper{opts: opts}
}

// Sweep laeuft genau einmal, nachdem der Cache gesynct ist. present enthaelt
// die UIDs, die der Cache kennt.
func (s *Sweeper) Sweep(ctx context.Context, present map[string]struct{}) error {
	logger := log.FromContext(ctx).WithName("resync")

	live, err := s.opts.Lister.LiveResources(ctx, s.opts.Cluster)
	if err != nil {
		return fmt.Errorf("load live resources: %w", err)
	}

	observedAt := s.opts.Now()
	for _, resource := range live {
		if _, stillThere := present[resource.UID]; stillThere {
			continue
		}
		logger.Info("waehrend des Ausfalls geloescht",
			"gvk", resource.Kind, "namespace", resource.Namespace,
			"name", resource.Name, "uid", resource.UID)

		change := store.Change{
			UID: resource.UID, Cluster: s.opts.Cluster,
			APIGroup: resource.APIGroup, APIVersion: resource.APIVersion,
			Kind: resource.Kind, Namespace: resource.Namespace, Name: resource.Name,
			Type: store.Deleted, Offline: true, ObservedAt: observedAt,
			Hash: resource.Hash, Object: resource.Object, Images: resource.Images,
		}
		select {
		case s.opts.Out <- change:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
