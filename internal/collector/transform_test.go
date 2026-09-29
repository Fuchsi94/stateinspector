package collector_test

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/Fuchsi94/stateinspector/internal/collector"
)

func configMap(data map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "web-config", "namespace": "demo"},
		"data":       data,
	}}
}

func transformed(t *testing.T, in any) *unstructured.Unstructured {
	t.Helper()
	out, err := collector.StripConfigMapValues(in)
	if err != nil {
		t.Fatalf("StripConfigMapValues(): %v", err)
	}
	u, ok := out.(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("Ergebnis ist %T, want *unstructured.Unstructured", out)
	}
	return u
}

// R9: die Werte duerfen den Cache gar nicht erst erreichen.
func TestTransformRemovesConfigMapValues(t *testing.T) {
	got := transformed(t, configMap(map[string]any{"LOG_LEVEL": "info", "PORT": "8080"}))

	data, ok := got.Object["data"].(map[string]any)
	if !ok {
		t.Fatalf("data fehlt oder hat den falschen Typ: %#v", got.Object["data"])
	}
	if len(data) != 2 || data["keys"] == nil || data["sha256"] == nil {
		t.Fatalf("data = %#v, want genau keys und sha256", data)
	}
	flat := strings.Join([]string{asString(data["sha256"]), joinKeys(data["keys"])}, " ")
	if strings.Contains(flat, "info") || strings.Contains(flat, "8080") {
		t.Errorf("ein ConfigMap-Wert hat den Transform ueberlebt: %s", flat)
	}
	if !strings.Contains(joinKeys(data["keys"]), "LOG_LEVEL") {
		t.Errorf("Schluessel fehlen: %v", data["keys"])
	}
}

func TestTransformHashIgnoresKeyOrderButNotValues(t *testing.T) {
	hash := func(data map[string]any) string {
		d, _ := transformed(t, configMap(data)).Object["data"].(map[string]any)
		return asString(d["sha256"])
	}
	base := hash(map[string]any{"a": "1", "b": "2"})
	if base != hash(map[string]any{"b": "2", "a": "1"}) {
		t.Error("Schluesselreihenfolge aendert den Hash")
	}
	if base == hash(map[string]any{"a": "1", "b": "3"}) {
		t.Error("geaenderter Wert aendert den Hash nicht")
	}
}

// Alles andere als eine ConfigMap muss unveraendert durchlaufen.
func TestTransformLeavesOtherKindsUntouched(t *testing.T) {
	deploy := deploymentObj("99999999-9999-9999-9999-999999999999", "nginx:1.27", "1")
	got := transformed(t, deploy)

	spec, _ := got.Object["spec"].(map[string]any)
	if spec == nil {
		t.Fatal("spec wurde entfernt")
	}
	if !strings.Contains(joinKeys(spec), "template") {
		t.Errorf("Deployment wurde veraendert: %#v", spec)
	}
}

// Ein unerwarteter Typ darf den Informer nicht mit einer Panic beenden.
func TestTransformRejectsUnexpectedType(t *testing.T) {
	if _, err := collector.StripConfigMapValues("kein Objekt"); err == nil {
		t.Error("StripConfigMapValues() akzeptierte einen String")
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func joinKeys(v any) string {
	switch typed := v.(type) {
	case []any:
		var parts []string
		for _, item := range typed {
			parts = append(parts, asString(item))
		}
		return strings.Join(parts, ",")
	case map[string]any:
		var parts []string
		for key := range typed {
			parts = append(parts, key)
		}
		return strings.Join(parts, ",")
	}
	return ""
}
