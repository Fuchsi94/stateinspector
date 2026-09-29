//go:build e2e

// Package e2e prueft den ganzen Weg gegen einen laufenden Cluster:
// kubectl aendert ein Deployment, der Collector schreibt es fort, und der
// MCP-Server beantwortet die Frage danach.
package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultEndpoint = "http://localhost:8081/mcp"
	imagePath       = "/spec/template/spec/containers/0/image"
)

// endpoint ist ueberschreibbar, damit CI einen anderen Port-Forward nutzen kann.
func endpoint() string {
	if v := strings.TrimSpace(os.Getenv("MCP_ENDPOINT")); v != "" {
		return v
	}
	return defaultEndpoint
}

// kubectlContext haelt den Zugriff auf den lokalen Cluster. CLAUDE.md verbietet
// kubectl gegen irgendeinen anderen Kontext.
func kubectlContext() string {
	if v := strings.TrimSpace(os.Getenv("E2E_CONTEXT")); v != "" {
		return v
	}
	return "kind-stateinspector"
}

func kubectl(t *testing.T, args ...string) string {
	t.Helper()
	full := append([]string{"--context", kubectlContext()}, args...)
	out, err := exec.CommandContext(t.Context(), "kubectl", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// connect baut eine MCP-Sitzung. Schlaegt das fehl, ist die Umgebung nicht
// bereit - und der Test sagt das, statt in einen nichtssagenden Timeout zu laufen.
func connect(t *testing.T) *mcpsdk.ClientSession {
	t.Helper()

	// Erst der Operator: ohne Ready ist alles Weitere Rauschen.
	kubectl(t, "-n", "stateinspector", "rollout", "status",
		"deployment/stateinspector", "--timeout=120s")

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "e2e", Version: "0"}, nil)
	transport := &mcpsdk.StreamableClientTransport{Endpoint: endpoint()}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	sess, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("MCP-Server unter %s nicht erreichbar: %v\n"+
			"Laeuft 'just up' (oder der Port-Forward aus 'just mcp')?", endpoint(), err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

type changeView struct {
	ObservedAt   string   `json:"observed_at"`
	ChangeType   string   `json:"change_type"`
	Kind         string   `json:"kind"`
	Namespace    string   `json:"namespace"`
	Name         string   `json:"name"`
	ChangedPaths []string `json:"changed_paths"`
	Images       []string `json:"images"`
}

func listChanges(t *testing.T, sess *mcpsdk.ClientSession, since string) []changeView {
	t.Helper()
	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "list_changes",
		Arguments: map[string]any{
			"since": since, "namespace": "demo", "kind": "Deployment", "name": "web",
		},
	})
	if err != nil {
		t.Fatalf("list_changes: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_changes meldet Fehler: %+v", res.Content)
	}

	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("Ergebnis serialisieren: %v", err)
	}
	var out struct {
		Changes []changeView `json:"changes"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Ergebnis lesen: %v (roh: %s)", err, raw)
	}
	return out.Changes
}

// waitForImageChange wartet auf den Eintrag, der genau diesen Image-Wechsel
// beschreibt und nach der uebergebenen Marke entstanden ist.
//
// Beide Einschraenkungen sind noetig. Nur auf das Image zu pruefen wuerde einen
// spaeteren Scale-Eintrag treffen, der dasselbe Image traegt. Und ohne Zeitmarke
// passt ein Eintrag aus einem frueheren Lauf, der noch im Fenster liegt - dann
// laeuft der Test weiter, bevor seine eigene Aenderung ueberhaupt da ist.
func waitForImageChange(t *testing.T, sess *mcpsdk.ClientSession, image string, after time.Time) changeView {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		for _, change := range listChanges(t, sess, "5m") {
			if change.ChangeType != "updated" || !hasPath(change, imagePath) || !hasImage(change, image) {
				continue
			}
			observed, err := time.Parse(time.RFC3339, change.ObservedAt)
			if err != nil {
				t.Fatalf("observed_at %q nicht lesbar: %v", change.ObservedAt, err)
			}
			if observed.After(after) {
				return change
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("kein updated-Eintrag mit %s und Image %q nach %s innerhalb von 90s",
		imagePath, image, after.Format(time.RFC3339Nano))
	return changeView{}
}

func hasPath(change changeView, want string) bool {
	for _, got := range change.ChangedPaths {
		if got == want {
			return true
		}
	}
	return false
}

func hasImage(change changeView, want string) bool {
	for _, got := range change.Images {
		if got == want {
			return true
		}
	}
	return false
}

// otherImage liefert ein Image, das sich vom aktuellen unterscheidet. Ein
// fester Zielwert wuerde je nach Reihenfolge der Tests gar keine Aenderung
// ausloesen - und der Test wartete dann auf etwas, das nie passiert.
func otherImage(t *testing.T) string {
	t.Helper()
	const a, b = "nginx:1.27", "nginx:1.27-alpine"
	current := strings.TrimSpace(kubectl(t, "-n", "demo", "get", "deployment/web",
		"-o", "jsonpath={.spec.template.spec.containers[0].image}"))
	if current == a {
		return b
	}
	return a
}

// Der Weg, um den es geht: eine echte Aenderung wird historisiert und ist
// ueber MCP auffindbar.
func TestImageChangeShowsUpInListChanges(t *testing.T) {
	sess := connect(t)

	target := otherImage(t)
	marker := time.Now().UTC()
	kubectl(t, "-n", "demo", "set", "image", "deployment/web", "nginx="+target)

	change := waitForImageChange(t, sess, target, marker)

	if change.ChangeType != "updated" {
		t.Errorf("change_type = %q, want updated", change.ChangeType)
	}
	if change.Namespace != "demo" || change.Name != "web" {
		t.Errorf("falsches Objekt: %s/%s", change.Namespace, change.Name)
	}
	var sawImagePath bool
	for _, path := range change.ChangedPaths {
		if path == imagePath {
			sawImagePath = true
		}
	}
	if !sawImagePath {
		t.Errorf("changed_paths = %v, want den Image-Pfad %s", change.ChangedPaths, imagePath)
	}
}

// R4 im Ganzen: dasselbe Image nochmal setzen ist keine Aenderung.
func TestSettingTheSameImageTwiceAddsNothing(t *testing.T) {
	sess := connect(t)

	target := otherImage(t)
	marker := time.Now().UTC()
	kubectl(t, "-n", "demo", "set", "image", "deployment/web", "nginx="+target)
	waitForImageChange(t, sess, target, marker)

	// Ab hier zaehlt nur noch, was neu dazukommt. Ein gleitendes Fenster waere
	// untauglich: daraus altern Eintraege zwischen zwei Messungen heraus, und
	// der Test wuerde sporadisch scheitern, ohne dass etwas kaputt ist.
	time.Sleep(3 * time.Second)
	cutoff := time.Now().UTC()

	kubectl(t, "-n", "demo", "set", "image", "deployment/web", "nginx="+target)
	time.Sleep(10 * time.Second)

	if after := listChanges(t, sess, cutoff.Format(time.RFC3339Nano)); len(after) != 0 {
		t.Errorf("%d neue Eintraege nach dem zweiten identischen set image, want 0: %+v",
			len(after), after)
	}
}
