package collector_test

import (
	"context"
	"testing"
	"time"

	"github.com/Fuchsi94/stateinspector/internal/collector"
	"github.com/Fuchsi94/stateinspector/internal/store"
)

// fakeLister liefert dem Sweep den gespeicherten Stand und haelt fest, fuer
// welche UIDs die vollen Objekte nachgeladen wurden.
type fakeLister struct {
	live       []store.LiveResource
	err        error
	fetchedFor []string
}

func (f *fakeLister) LiveUIDs(context.Context, string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	uids := make([]string, 0, len(f.live))
	for _, r := range f.live {
		uids = append(uids, r.UID)
	}
	return uids, nil
}

func (f *fakeLister) ResourcesByUID(_ context.Context, _ string, uids []string) ([]store.LiveResource, error) {
	f.fetchedFor = append(f.fetchedFor, uids...)
	wanted := map[string]struct{}{}
	for _, uid := range uids {
		wanted[uid] = struct{}{}
	}
	var out []store.LiveResource
	for _, r := range f.live {
		if _, ok := wanted[r.UID]; ok {
			out = append(out, r)
		}
	}
	return out, nil
}

func liveResource(uid, name string) store.LiveResource {
	return store.LiveResource{
		UID: uid, APIGroup: "apps", APIVersion: "apps/v1", Kind: "Deployment",
		Namespace: "demo", Name: name, Hash: "h",
		Object: map[string]any{"kind": "Deployment", "metadata": map[string]any{"name": name}},
	}
}

func newSweeper(lister collector.Lister, out chan store.Change) *collector.Sweeper {
	return collector.NewSweeper(collector.SweeperOptions{
		Cluster: "local",
		Lister:  lister,
		Out:     out,
		Now:     func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
}

// R13: was der Store fuer lebendig haelt, im Cache aber fehlt, wurde waehrend
// des Ausfalls geloescht.
func TestSweepMarksVanishedResourcesDeletedOffline(t *testing.T) {
	out := make(chan store.Change, 8)
	lister := &fakeLister{live: []store.LiveResource{
		liveResource("11111111-1111-1111-1111-111111111111", "web"),
		liveResource("22222222-2222-2222-2222-222222222222", "api"),
	}}
	present := map[string]struct{}{"11111111-1111-1111-1111-111111111111": {}}

	if err := newSweeper(lister, out).Sweep(t.Context(), present); err != nil {
		t.Fatalf("Sweep(): %v", err)
	}

	got := drain(t, out)
	if len(got) != 1 {
		t.Fatalf("%d Aenderungen, want 1: %+v", len(got), got)
	}
	if got[0].Type != store.Deleted {
		t.Errorf("Type = %q, want deleted", got[0].Type)
	}
	if !got[0].Offline {
		t.Error("Offline = false; die Loeschung passierte waehrend des Ausfalls")
	}
	if got[0].Name != "api" {
		t.Errorf("Name = %q, want api", got[0].Name)
	}
	// Nur das Fehlende wird nachgeladen, nicht der ganze Cluster.
	if len(lister.fetchedFor) != 1 || lister.fetchedFor[0] != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("volle Objekte geladen fuer %v, want nur die verschwundene UID", lister.fetchedFor)
	}
	if len(got[0].Object) == 0 {
		t.Error("Object ist leer; der letzte bekannte Zustand fehlt")
	}
}

// Laeuft alles noch, entsteht nichts.
func TestSweepWithNothingMissingProducesNoChange(t *testing.T) {
	out := make(chan store.Change, 8)
	uid := "33333333-3333-3333-3333-333333333333"
	lister := &fakeLister{live: []store.LiveResource{liveResource(uid, "web")}}

	if err := newSweeper(lister, out).Sweep(t.Context(), map[string]struct{}{uid: {}}); err != nil {
		t.Fatalf("Sweep(): %v", err)
	}
	if got := drain(t, out); len(got) != 0 {
		t.Errorf("%d Aenderungen, want 0: %+v", len(got), got)
	}
	if len(lister.fetchedFor) != 0 {
		t.Errorf("volle Objekte geladen fuer %v, obwohl nichts fehlt", lister.fetchedFor)
	}
}

// Bereits als geloescht markierte Objekte liefert der Store gar nicht mehr,
// also darf ein zweiter Sweep nichts erzeugen.
func TestSweepIsIdempotentAcrossRestarts(t *testing.T) {
	out := make(chan store.Change, 8)
	lister := &fakeLister{live: nil}

	if err := newSweeper(lister, out).Sweep(t.Context(), map[string]struct{}{}); err != nil {
		t.Fatalf("Sweep(): %v", err)
	}
	if got := drain(t, out); len(got) != 0 {
		t.Errorf("%d Aenderungen, want 0", len(got))
	}
}
