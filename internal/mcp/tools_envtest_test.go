//go:build envtest

package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Fuchsi94/stateinspector/internal/dbtest"
	"github.com/Fuchsi94/stateinspector/internal/mcp"
	"github.com/Fuchsi94/stateinspector/internal/store"
)

var databaseURL string

// now ist der feste "Jetzt"-Zeitpunkt aller Tests, damit relative Angaben
// wie 24h reproduzierbar sind.
var now = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func TestMain(m *testing.M) {
	ctx := context.Background()
	url, stop, err := dbtest.StartPostgres(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	databaseURL = url

	code := m.Run()
	stop()
	os.Exit(code)
}

// session baut Store, Server und einen In-Process-Client des SDK.
func session(t *testing.T, seed ...store.Change) *mcpsdk.ClientSession {
	t.Helper()
	ctx := t.Context()

	db, err := store.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("store.New(): %v", err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(): %v", err)
	}
	if err := dbtest.Truncate(ctx, databaseURL); err != nil {
		t.Fatalf("Tabellen leeren: %v", err)
	}
	if len(seed) > 0 {
		if err := db.WriteChanges(ctx, seed); err != nil {
			t.Fatalf("Seed schreiben: %v", err)
		}
	}

	srv := mcp.NewServer(mcp.Options{
		Backend: db, Cluster: "local", Now: func() time.Time { return now },
	}).MCPServer()

	clientSide, serverSide := mcpsdk.NewInMemoryTransports()
	go func() { _ = srv.Run(ctx, serverSide) }()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "0"}, nil)
	sess, err := client.Connect(ctx, clientSide, nil)
	if err != nil {
		t.Fatalf("Connect(): %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func call(t *testing.T, sess *mcpsdk.ClientSession, tool string, args map[string]any, out any) {
	t.Helper()
	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", tool, err)
	}
	if res.IsError {
		t.Fatalf("CallTool(%s) meldet Fehler: %+v", tool, res.Content)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("Ergebnis serialisieren: %v", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("Ergebnis %s lesen: %v (roh: %s)", tool, err, raw)
	}
}

func callExpectingError(t *testing.T, sess *mcpsdk.ClientSession, tool string, args map[string]any) {
	t.Helper()
	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: tool, Arguments: args})
	if err == nil && !res.IsError {
		t.Fatalf("CallTool(%s) mit %v lieferte keinen Fehler", tool, args)
	}
}

func seedChange(uid, name string, typ store.ChangeType, at time.Time, image string) store.Change {
	return store.Change{
		UID: uid, Cluster: "local", APIGroup: "apps", APIVersion: "apps/v1",
		Kind: "Deployment", Namespace: "demo", Name: name,
		Type: typ, ObservedAt: at, Hash: image,
		Object: map[string]any{
			"apiVersion": "apps/v1", "kind": "Deployment",
			"metadata": map[string]any{"name": name, "namespace": "demo"},
			"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
				"containers": []any{map[string]any{"name": "nginx", "image": image}},
			}}},
		},
		Images:       []string{image},
		ChangedPaths: []string{"/spec/template/spec/containers/0/image"},
	}
}

func TestListChangesRespectsWindowAndOmitsObject(t *testing.T) {
	uid := uuid.NewString()
	sess := session(t,
		seedChange(uid, "web", store.Created, now.Add(-48*time.Hour), "nginx:1.25"),
		seedChange(uid, "web", store.Updated, now.Add(-2*time.Hour), "nginx:1.27"),
	)

	var out struct {
		Changes   []map[string]any `json:"changes"`
		Truncated bool             `json:"truncated"`
	}
	call(t, sess, "list_changes", map[string]any{"since": "24h"}, &out)

	if len(out.Changes) != 1 {
		t.Fatalf("%d Aenderungen im 24h-Fenster, want 1: %+v", len(out.Changes), out.Changes)
	}
	entry := out.Changes[0]
	if entry["change_type"] != "updated" {
		t.Errorf("change_type = %v, want updated", entry["change_type"])
	}
	if paths, _ := entry["changed_paths"].([]any); len(paths) == 0 {
		t.Error("changed_paths fehlt")
	}
	// R14: die Listenansicht traegt das Objekt bewusst nicht. Geprueft wird das
	// Fehlen des Feldes, nicht ein Substring - "containers" steht legitim im
	// JSON-Pointer eines geaenderten Pfades.
	if _, present := entry["object"]; present {
		raw, _ := json.Marshal(entry)
		t.Errorf("list_changes liefert das vollstaendige Objekt: %s", raw)
	}
}

func TestGetResourceHonoursTimestamp(t *testing.T) {
	uid := uuid.NewString()
	sess := session(t,
		seedChange(uid, "web", store.Created, now.Add(-48*time.Hour), "nginx:1.25"),
		seedChange(uid, "web", store.Updated, now.Add(-2*time.Hour), "nginx:1.27"),
	)

	var current struct {
		Found      bool           `json:"found"`
		ObservedAt string         `json:"observed_at"`
		Object     map[string]any `json:"object"`
	}
	call(t, sess, "get_resource", map[string]any{
		"kind": "Deployment", "namespace": "demo", "name": "web",
	}, &current)
	if !current.Found || imageOf(current.Object) != "nginx:1.27" {
		t.Errorf("ohne at: %v / %q, want nginx:1.27", current.Found, imageOf(current.Object))
	}

	var past struct {
		Found      bool           `json:"found"`
		ObservedAt string         `json:"observed_at"`
		Object     map[string]any `json:"object"`
	}
	call(t, sess, "get_resource", map[string]any{
		"kind": "Deployment", "namespace": "demo", "name": "web", "at": "24h",
	}, &past)
	if !past.Found || imageOf(past.Object) != "nginx:1.25" {
		t.Errorf("mit at=24h: %v / %q, want nginx:1.25", past.Found, imageOf(past.Object))
	}
	if past.ObservedAt == current.ObservedAt {
		t.Error("observed_at ist fuer beide Zeitpunkte gleich")
	}
}

// Ein unbekanntes Objekt ist kein Fehler, sondern eine Antwort.
func TestGetResourceUnknownIsNotAnError(t *testing.T) {
	sess := session(t)

	var out struct {
		Found bool   `json:"found"`
		Note  string `json:"note"`
	}
	call(t, sess, "get_resource", map[string]any{
		"kind": "Deployment", "namespace": "demo", "name": "gibtsnicht",
	}, &out)

	if out.Found {
		t.Error("found = true fuer ein unbekanntes Objekt")
	}
	if out.Note == "" {
		t.Error("keine Erklaerung im Ergebnis")
	}
}

func TestDiffResourceShowsImagePath(t *testing.T) {
	uid := uuid.NewString()
	sess := session(t,
		seedChange(uid, "web", store.Created, now.Add(-48*time.Hour), "nginx:1.25"),
		seedChange(uid, "web", store.Updated, now.Add(-2*time.Hour), "nginx:1.27"),
	)

	var out struct {
		Found        bool     `json:"found"`
		ChangedPaths []string `json:"changed_paths"`
		Patch        any      `json:"patch"`
	}
	call(t, sess, "diff_resource", map[string]any{
		"kind": "Deployment", "namespace": "demo", "name": "web", "from": "24h",
	}, &out)

	if !out.Found {
		t.Fatal("diff_resource fand die Versionen nicht")
	}
	want := "/spec/template/spec/containers/0/image"
	if len(out.ChangedPaths) != 1 || out.ChangedPaths[0] != want {
		t.Errorf("changed_paths = %v, want [%s]", out.ChangedPaths, want)
	}
	if out.Patch == nil {
		t.Error("patch fehlt")
	}
}

func TestListResourcesHidesDeletedUnlessAsked(t *testing.T) {
	alive, gone := uuid.NewString(), uuid.NewString()
	sess := session(t,
		seedChange(alive, "web", store.Created, now.Add(-3*time.Hour), "nginx:1.27"),
		seedChange(gone, "old", store.Created, now.Add(-3*time.Hour), "nginx:1.25"),
		seedChange(gone, "old", store.Deleted, now.Add(-time.Hour), "nginx:1.25"),
	)

	var visible struct {
		Resources []struct {
			Name      string `json:"name"`
			DeletedAt string `json:"deleted_at"`
		} `json:"resources"`
	}
	call(t, sess, "list_resources", map[string]any{}, &visible)
	if len(visible.Resources) != 1 || visible.Resources[0].Name != "web" {
		t.Fatalf("ohne include_deleted: %+v, want nur web", visible.Resources)
	}

	var all struct {
		Resources []struct {
			Name      string `json:"name"`
			DeletedAt string `json:"deleted_at"`
		} `json:"resources"`
	}
	call(t, sess, "list_resources", map[string]any{"include_deleted": true}, &all)
	if len(all.Resources) != 2 {
		t.Fatalf("mit include_deleted: %+v, want zwei", all.Resources)
	}
	var sawDeletedAt bool
	for _, r := range all.Resources {
		if r.Name == "old" && r.DeletedAt != "" {
			sawDeletedAt = true
		}
	}
	if !sawDeletedAt {
		t.Error("deleted_at fehlt beim geloeschten Objekt")
	}
}

func TestGetVersionsListsRunningImages(t *testing.T) {
	sess := session(t,
		seedChange(uuid.NewString(), "web", store.Created, now.Add(-time.Hour), "nginx:1.27"),
	)

	var out struct {
		Workloads []struct {
			Name   string   `json:"name"`
			Images []string `json:"images"`
		} `json:"workloads"`
	}
	call(t, sess, "get_versions", map[string]any{"name_contains": "we"}, &out)

	if len(out.Workloads) != 1 || out.Workloads[0].Images[0] != "nginx:1.27" {
		t.Errorf("get_versions = %+v, want web mit nginx:1.27", out.Workloads)
	}
}

// R18: ueber der harten Grenze wird gekappt und das sichtbar gemacht.
func TestListChangesCapsAtFiveHundred(t *testing.T) {
	seed := make([]store.Change, 0, 600)
	for i := 0; i < 600; i++ {
		seed = append(seed, seedChange(uuid.NewString(), fmt.Sprintf("web-%03d", i),
			store.Created, now.Add(-time.Duration(i)*time.Second), "nginx:1.27"))
	}
	sess := session(t, seed...)

	var out struct {
		Changes   []any  `json:"changes"`
		Truncated bool   `json:"truncated"`
		Note      string `json:"note"`
	}
	call(t, sess, "list_changes", map[string]any{"since": "24h", "limit": 5000}, &out)

	if len(out.Changes) != 500 {
		t.Errorf("%d Eintraege geliefert, want 500", len(out.Changes))
	}
	if !out.Truncated {
		t.Error("truncated = false, obwohl abgeschnitten wurde")
	}
	if out.Note == "" {
		t.Error("kein Hinweis auf die Kuerzung")
	}
}

func TestToolsRejectUnreadableTimes(t *testing.T) {
	sess := session(t)
	callExpectingError(t, sess, "list_changes", map[string]any{"since": "gestern"})
	callExpectingError(t, sess, "get_resource", map[string]any{
		"kind": "Deployment", "namespace": "demo", "name": "web", "at": "7x",
	})
}

func imageOf(object map[string]any) string {
	spec, _ := object["spec"].(map[string]any)
	tmpl, _ := spec["template"].(map[string]any)
	pod, _ := tmpl["spec"].(map[string]any)
	containers, _ := pod["containers"].([]any)
	if len(containers) == 0 {
		return ""
	}
	first, _ := containers[0].(map[string]any)
	image, _ := first["image"].(string)
	return image
}
