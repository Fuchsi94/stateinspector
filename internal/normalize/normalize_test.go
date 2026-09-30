package normalize_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/Fuchsi94/stateinspector/internal/normalize"
)

func loadYAML(t *testing.T, path string) *unstructured.Unstructured {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // Pfad stammt aus dem Glob ueber testdata
	if err != nil {
		t.Fatalf("%s lesen: %v", path, err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s parsen: %v", path, err)
	}
	return &unstructured.Unstructured{Object: m}
}

// Golden-Tests: rohes Objekt rein, erwartetes normalisiertes Objekt raus.
func TestGolden(t *testing.T) {
	dirs, err := filepath.Glob("testdata/golden/*/*")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("keine Golden-Faelle gefunden: %v", err)
	}
	for _, dir := range dirs {
		t.Run(strings.TrimPrefix(dir, "testdata/golden/"), func(t *testing.T) {
			got, err := normalize.Object(loadYAML(t, filepath.Join(dir, "input.yaml")))
			if err != nil {
				t.Fatalf("Object() Fehler: %v", err)
			}
			gotJSON, err := json.MarshalIndent(got, "", "  ")
			if err != nil {
				t.Fatalf("Ergebnis serialisieren: %v", err)
			}
			wantRaw, err := os.ReadFile(filepath.Join(dir, "want.json")) //nolint:gosec // dito
			if err != nil {
				t.Fatalf("want.json lesen: %v", err)
			}
			var want any
			if err := json.Unmarshal(wantRaw, &want); err != nil {
				t.Fatalf("want.json parsen: %v", err)
			}
			wantJSON, err := json.MarshalIndent(want, "", "  ")
			if err != nil {
				t.Fatalf("want serialisieren: %v", err)
			}
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("normalisiertes Objekt weicht ab.\n--- got ---\n%s\n--- want ---\n%s", gotJSON, wantJSON)
			}
		})
	}
}

// Pflichtfall pro Art (R6/R7): aendert sich nur Rauschen, bleibt der Hash gleich.
func TestNoiseOnlyKeepsHashStable(t *testing.T) {
	dirs, err := filepath.Glob("testdata/noise/*")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("keine Rausch-Faelle gefunden: %v", err)
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			hashA := mustHash(t, loadYAML(t, filepath.Join(dir, "a.yaml")))
			hashB := mustHash(t, loadYAML(t, filepath.Join(dir, "b.yaml")))
			if hashA != hashB {
				t.Errorf("nur Rauschen geaendert, Hash unterscheidet sich: %s != %s", hashA, hashB)
			}
		})
	}
}

func mustHash(t *testing.T, u *unstructured.Unstructured) string {
	t.Helper()
	obj, err := normalize.Object(u)
	if err != nil {
		t.Fatalf("Object() Fehler: %v", err)
	}
	h, err := normalize.Hash(obj)
	if err != nil {
		t.Fatalf("Hash() Fehler: %v", err)
	}
	return h
}

// R7: restartedAt markiert einen echten Rollout und muss erhalten bleiben.
func TestRestartedAtSurvivesAndChangesHash(t *testing.T) {
	base := loadYAML(t, "testdata/noise/deployment/a.yaml")
	restarted := loadYAML(t, "testdata/noise/deployment/a.yaml")
	if err := unstructured.SetNestedField(restarted.Object,
		"2026-09-29T10:00:00Z",
		"spec", "template", "metadata", "annotations", "kubectl.kubernetes.io/restartedAt"); err != nil {
		t.Fatalf("Annotation setzen: %v", err)
	}

	obj, err := normalize.Object(restarted)
	if err != nil {
		t.Fatalf("Object() Fehler: %v", err)
	}
	if !strings.Contains(string(mustJSON(t, obj)), "restartedAt") {
		t.Error("restartedAt wurde entfernt, ist aber ein echter Rollout")
	}
	if mustHash(t, base) == mustHash(t, restarted) {
		t.Error("restartedAt aendert den Hash nicht, muesste es aber")
	}
}

// R8: ConfigMap-Werte tauchen im normalisierten Objekt nicht auf.
func TestConfigMapValuesNeverAppear(t *testing.T) {
	obj, err := normalize.Object(loadYAML(t, "testdata/golden/configmap/basic/input.yaml"))
	if err != nil {
		t.Fatalf("Object() Fehler: %v", err)
	}
	out := string(mustJSON(t, obj))

	if strings.Contains(out, "info") {
		t.Errorf("ConfigMap-Wert steht im normalisierten Objekt: %s", out)
	}
	if !strings.Contains(out, "LOG_LEVEL") {
		t.Errorf("ConfigMap-Schluessel fehlt in der Keys-Liste: %s", out)
	}
	if !strings.Contains(out, "sha256") {
		t.Errorf("ConfigMap-Hash fehlt: %s", out)
	}
}

func TestConfigMapHashIgnoresKeyOrderButNotValues(t *testing.T) {
	mk := func(data map[string]any) string {
		u := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata":   map[string]any{"name": "c", "namespace": "demo"},
			"data":       data,
		}}
		return mustHash(t, u)
	}
	same := mk(map[string]any{"a": "1", "b": "2"})
	reordered := mk(map[string]any{"b": "2", "a": "1"})
	changed := mk(map[string]any{"a": "1", "b": "3"})

	if same != reordered {
		t.Error("gleicher Inhalt in anderer Schluesselreihenfolge ergibt anderen Hash")
	}
	if same == changed {
		t.Error("geaenderter Wert bei gleichen Schluesseln ergibt gleichen Hash")
	}
}

// R6: {} und "fehlt" muessen identisch hashen.
func TestEmptyCollectionsHashLikeAbsent(t *testing.T) {
	withEmpty := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata":   map[string]any{"name": "s", "namespace": "demo", "labels": map[string]any{}},
		"spec":       map[string]any{"ports": []any{}, "clusterIP": "10.0.0.1"},
	}}
	without := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata":   map[string]any{"name": "s", "namespace": "demo"},
		"spec":       map[string]any{"clusterIP": "10.0.0.1"},
	}}
	if mustHash(t, withEmpty) != mustHash(t, without) {
		t.Error("leere Map/Liste hasht anders als ein fehlendes Feld")
	}
}

func TestHashIsStableAcrossKeyOrder(t *testing.T) {
	a := map[string]any{"kind": "Service", "apiVersion": "v1", "metadata": map[string]any{"name": "s"}}
	b := map[string]any{"metadata": map[string]any{"name": "s"}, "apiVersion": "v1", "kind": "Service"}

	ha, err := normalize.Hash(a)
	if err != nil {
		t.Fatalf("Hash(a): %v", err)
	}
	hb, err := normalize.Hash(b)
	if err != nil {
		t.Fatalf("Hash(b): %v", err)
	}
	if ha != hb {
		t.Errorf("Schluesselreihenfolge aendert den Hash: %s != %s", ha, hb)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("serialisieren: %v", err)
	}
	return b
}
