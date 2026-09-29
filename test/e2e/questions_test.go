//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// historyStart liefert den Zeitstempel der aeltesten bekannten Version von
// demo/web. Relative Angaben wie "1h" waeren in einem frisch aufgesetzten
// Cluster aelter als die Historie - der Test wuerde dann gruen bleiben, obwohl
// die Zeitreise gar nicht geprueft wurde.
func historyStart(t *testing.T, sess *mcpsdk.ClientSession) string {
	t.Helper()
	changes := listChanges(t, sess, "24h")
	if len(changes) == 0 {
		t.Fatal("keine Historie fuer demo/web; laeuft der Collector?")
	}
	oldest := changes[len(changes)-1].ObservedAt
	if oldest == "" {
		t.Fatal("aeltester Eintrag ohne observed_at")
	}
	return oldest
}

// TestLeadingQuestions prueft das Abnahmekriterium wortwoertlich: die vier
// Fragen aus dem Auftrag muessen ueber die Tools beantwortbar sein. Der Test
// gibt die Antworten aus, damit sie ins README uebernommen werden koennen.
func TestLeadingQuestions(t *testing.T) {
	sess := connect(t)

	// Eine frische Aenderung, damit es ueberhaupt einen Rollout zu zeigen gibt.
	target := otherImage(t)
	marker := time.Now().UTC()
	kubectl(t, "-n", "demo", "set", "image", "deployment/web", "nginx="+target)
	waitForImageChange(t, sess, target, marker)
	since := historyStart(t, sess)

	questions := []struct {
		question string
		tool     string
		args     map[string]any
		// wantFound verlangt eine inhaltliche Antwort, nicht nur ein Ergebnis.
		wantFound bool
	}{
		{
			"Was hat sich seit gestern in Namespace demo geaendert?",
			"list_changes", map[string]any{"since": "24h", "namespace": "demo", "limit": 5},
			false,
		},
		{
			"Welche Image-Version laeuft von web?",
			"get_versions", map[string]any{"namespace": "demo", "name_contains": "web"},
			false,
		},
		{
			"Wie sah Deployment web zu Beginn der Historie aus?",
			"get_resource", map[string]any{"kind": "Deployment", "namespace": "demo", "name": "web", "at": since},
			true,
		},
		{
			"Was genau wurde seither an web geaendert?",
			"diff_resource", map[string]any{"kind": "Deployment", "namespace": "demo", "name": "web", "from": since},
			true,
		},
	}

	for _, q := range questions {
		t.Run(q.tool, func(t *testing.T) {
			res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
				Name: q.tool, Arguments: q.args,
			})
			if err != nil {
				t.Fatalf("%s: %v", q.tool, err)
			}
			if res.IsError {
				t.Fatalf("%s meldet Fehler: %+v", q.tool, res.Content)
			}
			if res.StructuredContent == nil {
				t.Fatalf("%s lieferte kein strukturiertes Ergebnis", q.tool)
			}
			pretty, err := json.MarshalIndent(res.StructuredContent, "", "  ")
			if err != nil {
				t.Fatalf("Ergebnis serialisieren: %v", err)
			}
			if q.wantFound {
				var body struct {
					Found bool `json:"found"`
				}
				if err := json.Unmarshal(pretty, &body); err != nil {
					t.Fatalf("Ergebnis lesen: %v", err)
				}
				if !body.Found {
					t.Errorf("%s fand nichts, obwohl die Historie den Zeitpunkt abdeckt:\n%s", q.tool, pretty)
				}
			}
			t.Logf("\nFrage: %s\nTool:  %s\nAntwort:\n%s", q.question, q.tool, pretty)
		})
	}
}
