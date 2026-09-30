package collector_test

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/Fuchsi94/stateinspector/internal/collector"
	"github.com/Fuchsi94/stateinspector/internal/normalize"
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

// Regression: im Betrieb laeuft ein ConfigMap erst durch den Cache-Transform
// und danach durch normalize.Object. Fasst die Normalisierungsregel das
// Ergebnis ein zweites Mal zusammen, steht in der Datenbank dauerhaft
// keys: ["keys","sha256"] statt der echten Schluesselnamen.
//
// Kein bisheriger Test lief diesen Weg: der Golden-Test fuettert ein rohes
// Objekt, der envtest prueft nur die Abwesenheit von Werten.
func TestTransformThenNormalizeKeepsRealKeyNames(t *testing.T) {
	raw := configMap(map[string]any{"LOG_LEVEL": "info", "PORT": "8080"})

	fromCache := transformed(t, raw)
	normalized, err := normalize.Object(fromCache)
	if err != nil {
		t.Fatalf("normalize.Object(): %v", err)
	}

	data, ok := normalized["data"].(map[string]any)
	if !ok {
		t.Fatalf("data fehlt: %#v", normalized["data"])
	}
	got := joinKeys(data["keys"])
	if !strings.Contains(got, "LOG_LEVEL") || !strings.Contains(got, "PORT") {
		t.Errorf("keys = %q, want die echten Schluesselnamen", got)
	}
	if strings.Contains(got, "sha256") {
		t.Errorf("keys = %q; das Feld wurde ein zweites Mal zusammengefasst", got)
	}

	// Und die Werte bleiben trotzdem draussen.
	flat := got + " " + asString(data["sha256"])
	if strings.Contains(flat, "info") || strings.Contains(flat, "8080") {
		t.Errorf("ein ConfigMap-Wert ist durchgerutscht: %s", flat)
	}
}

// Eine ConfigMap, die zufaellig die Schluessel keys und sha256 traegt, darf
// nicht faelschlich als bereits zusammengefasst gelten.
func TestTransformHandlesConfigMapWithColludingKeyNames(t *testing.T) {
	got := transformed(t, configMap(map[string]any{
		"keys": "a,b", "sha256": strings.Repeat("f", 64),
	}))

	data, _ := got.Object["data"].(map[string]any)
	list, isList := data["keys"].([]any)
	if !isList || len(list) != 2 {
		t.Fatalf("keys = %#v, want die zwei echten Schluesselnamen als Liste", data["keys"])
	}
	if strings.Contains(joinKeys(data["keys"]), "a,b") {
		t.Errorf("der Wert a,b steht in der Schluesselliste: %v", data["keys"])
	}
}

// R8 nennt binaryData ausdruecklich neben data, bisher hat es kein Test angefasst.
func TestTransformStripsBinaryDataToo(t *testing.T) {
	cm := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "certs", "namespace": "demo"},
		"binaryData": map[string]any{"tls.crt": "Z2VoZWltZXItc2NobHVlc3NlbA=="},
	}}

	got := transformed(t, cm)
	data, ok := got.Object["binaryData"].(map[string]any)
	if !ok {
		t.Fatalf("binaryData fehlt oder hat den falschen Typ: %#v", got.Object["binaryData"])
	}
	if !strings.Contains(joinKeys(data["keys"]), "tls.crt") {
		t.Errorf("Schluessel fehlt: %v", data["keys"])
	}
	if asString(data["sha256"]) == "" {
		t.Error("kein Hash fuer binaryData")
	}
	if strings.Contains(joinKeys(data["keys"])+asString(data["sha256"]), "Z2VoZWlt") {
		t.Error("der base64-Wert hat den Transform ueberlebt")
	}

	// Und auch hier darf die Normalisierung nicht ein zweites Mal zusammenfassen.
	normalized, err := normalize.Object(got)
	if err != nil {
		t.Fatalf("normalize.Object(): %v", err)
	}
	after, _ := normalized["binaryData"].(map[string]any)
	if !strings.Contains(joinKeys(after["keys"]), "tls.crt") {
		t.Errorf("binaryData-Schluessel nach der Normalisierung: %v, want tls.crt", after["keys"])
	}
}
