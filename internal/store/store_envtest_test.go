//go:build envtest

package store_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Fuchsi94/stateinspector/internal/store"
)

var databaseURL string

func TestMain(m *testing.M) {
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("stateinspector"),
		tcpostgres.WithUsername("stateinspector"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(2*time.Minute)),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Postgres-Container starten: %v\n", err)
		os.Exit(1)
	}
	databaseURL, err = container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Connection-String: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	_ = testcontainers.TerminateContainer(container)
	os.Exit(code)
}

// freshStore gibt jedem Test ein frisch migriertes, leeres Schema.
func freshStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := t.Context()
	s, err := store.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("store.New(): %v", err)
	}
	t.Cleanup(s.Close)

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(): %v", err)
	}
	if _, err := assertPool(t).Exec(ctx, "TRUNCATE changes, resources"); err != nil {
		t.Fatalf("Tabellen leeren: %v", err)
	}
	return s
}

// assertPool ist eine vom Store unabhaengige Verbindung. Die Tests pruefen
// damit den echten Datenbankzustand statt die Buchhaltung des Codes, den sie
// testen sollen.
func assertPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("Pruefverbindung oeffnen: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func scalar[T any](t *testing.T, sql string) T {
	t.Helper()
	var out T
	if err := assertPool(t).QueryRow(t.Context(), sql).Scan(&out); err != nil {
		t.Fatalf("Query %q: %v", sql, err)
	}
	return out
}

func change(uid string, typ store.ChangeType, at time.Time, image string) store.Change {
	return store.Change{
		UID:        uid,
		Cluster:    "local",
		APIGroup:   "apps",
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Namespace:  "demo",
		Name:       "web",
		Type:       typ,
		ObservedAt: at,
		Hash:       image,
		Object: map[string]any{
			"kind": "Deployment",
			"spec": map[string]any{"image": image},
		},
		Images: []string{image},
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := t.Context()
	s := freshStore(t)

	// Zweiter Lauf auf bereits migrierter Datenbank darf nicht scheitern.
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("zweiter Migrate() schlug fehl: %v", err)
	}
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("Ping(): %v", err)
	}
}

// Der CHECK-Constraint ist die letzte Verteidigung gegen einen unbekannten Typ.
func TestUnknownChangeTypeIsRejected(t *testing.T) {
	ctx := t.Context()
	s := freshStore(t)

	err := s.WriteChanges(ctx, []store.Change{
		change(uuid.NewString(), store.ChangeType("purged"), time.Now(), "nginx:1.27"),
	})
	if err == nil {
		t.Fatal("WriteChanges() akzeptierte change_type 'purged'")
	}
}

func TestBatchWriteLandsCompletely(t *testing.T) {
	ctx := t.Context()
	s := freshStore(t)

	const n = 150
	base := time.Now().UTC().Add(-time.Hour)
	batch := make([]store.Change, 0, n)
	for i := 0; i < n; i++ {
		batch = append(batch, change(uuid.NewString(), store.Created,
			base.Add(time.Duration(i)*time.Second), fmt.Sprintf("nginx:1.%d", i)))
	}
	if err := s.WriteChanges(ctx, batch); err != nil {
		t.Fatalf("WriteChanges(): %v", err)
	}

	if got := scalar[int64](t, "SELECT count(*) FROM changes"); got != n {
		t.Errorf("changes enthaelt %d Zeilen, want %d", got, n)
	}
	if got := scalar[int64](t, "SELECT count(*) FROM resources"); got != n {
		t.Errorf("resources enthaelt %d Zeilen, want %d", got, n)
	}
}

// R22/Nicht-Ziel: actor wird angelegt, bleibt aber leer.
func TestActorStaysNull(t *testing.T) {
	ctx := t.Context()
	s := freshStore(t)

	if err := s.WriteChanges(ctx, []store.Change{
		change(uuid.NewString(), store.Created, time.Now(), "nginx:1.27"),
	}); err != nil {
		t.Fatalf("WriteChanges(): %v", err)
	}

	if got := scalar[int64](t, "SELECT count(*) FROM changes WHERE actor IS NULL"); got != 1 {
		t.Errorf("actor ist nicht NULL: %d von 1 Zeilen", got)
	}
}

// R15: der Zeitpunkt entscheidet, nicht die Aktualitaet.
func TestObjectAtReturnsVersionValidAtTimestamp(t *testing.T) {
	ctx := t.Context()
	s := freshStore(t)

	uid := uuid.NewString()
	t0 := time.Now().UTC().Add(-3 * time.Hour)
	t1 := t0.Add(time.Hour)
	t2 := t1.Add(time.Hour)
	if err := s.WriteChanges(ctx, []store.Change{
		change(uid, store.Created, t0, "nginx:1.25"),
		change(uid, store.Updated, t1, "nginx:1.26"),
		change(uid, store.Updated, t2, "nginx:1.27"),
	}); err != nil {
		t.Fatalf("WriteChanges(): %v", err)
	}

	obj, observedAt, found, err := s.ObjectAt(ctx, "local", "Deployment", "demo", "web", t1.Add(time.Minute))
	if err != nil {
		t.Fatalf("ObjectAt(): %v", err)
	}
	if !found {
		t.Fatal("ObjectAt() fand keine Version")
	}
	spec, _ := obj["spec"].(map[string]any)
	if spec["image"] != "nginx:1.26" {
		t.Errorf("ObjectAt() lieferte %v, want die zum Zeitpunkt gueltige nginx:1.26", spec["image"])
	}
	if !observedAt.Equal(t1.Truncate(time.Microsecond)) {
		t.Errorf("observedAt = %s, want %s", observedAt, t1)
	}
}

func TestObjectAtAfterDeletionReturnsNothing(t *testing.T) {
	ctx := t.Context()
	s := freshStore(t)

	uid := uuid.NewString()
	created := time.Now().UTC().Add(-2 * time.Hour)
	deleted := created.Add(time.Hour)
	if err := s.WriteChanges(ctx, []store.Change{
		change(uid, store.Created, created, "nginx:1.27"),
		change(uid, store.Deleted, deleted, "nginx:1.27"),
	}); err != nil {
		t.Fatalf("WriteChanges(): %v", err)
	}

	if _, _, found, err := s.ObjectAt(ctx, "local", "Deployment", "demo", "web", deleted.Add(time.Minute)); err != nil {
		t.Fatalf("ObjectAt() nach Loeschung: %v", err)
	} else if found {
		t.Error("ObjectAt() lieferte ein Objekt, das zu diesem Zeitpunkt geloescht war")
	}

	// Vor der Loeschung existierte es sehr wohl.
	if _, _, found, err := s.ObjectAt(ctx, "local", "Deployment", "demo", "web", created.Add(time.Minute)); err != nil {
		t.Fatalf("ObjectAt() vor Loeschung: %v", err)
	} else if !found {
		t.Error("ObjectAt() fand das Objekt vor der Loeschung nicht")
	}
}

func TestCurrentAndLiveResourcesTrackDeletion(t *testing.T) {
	ctx := t.Context()
	s := freshStore(t)

	uid := uuid.NewString()
	now := time.Now().UTC()
	if err := s.WriteChanges(ctx, []store.Change{change(uid, store.Created, now, "nginx:1.27")}); err != nil {
		t.Fatalf("WriteChanges(): %v", err)
	}

	snap, found, err := s.Current(ctx, uid)
	if err != nil || !found {
		t.Fatalf("Current() = %v, found=%v, err=%v", snap, found, err)
	}
	if snap.Hash != "nginx:1.27" {
		t.Errorf("Current().Hash = %q, want nginx:1.27", snap.Hash)
	}
	live, err := s.LiveResources(ctx, "local")
	if err != nil {
		t.Fatalf("LiveResources(): %v", err)
	}
	if len(live) != 1 || live[0].UID != uid {
		t.Fatalf("LiveResources() = %+v, want genau das lebende Objekt", live)
	}
	if live[0].Kind != "Deployment" || live[0].Name != "web" || len(live[0].Object) == 0 {
		t.Errorf("LiveResources() liefert unvollstaendige Identitaet: %+v", live[0])
	}

	if err := s.WriteChanges(ctx, []store.Change{
		change(uid, store.Deleted, now.Add(time.Minute), "nginx:1.27"),
	}); err != nil {
		t.Fatalf("WriteChanges() Loeschung: %v", err)
	}
	if _, found, _ := s.Current(ctx, uid); found {
		t.Error("Current() liefert ein geloeschtes Objekt")
	}
	live, err = s.LiveResources(ctx, "local")
	if err != nil {
		t.Fatalf("LiveResources(): %v", err)
	}
	if len(live) != 0 {
		t.Errorf("LiveResources() enthaelt ein geloeschtes Objekt: %+v", live)
	}
}
