package diff_test

import (
	"testing"

	"github.com/Fuchsi94/stateinspector/internal/diff"
)

func deployment(image string, extra ...func(map[string]any)) map[string]any {
	obj := map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": "web", "namespace": "demo"},
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []any{
						map[string]any{"name": "nginx", "image": image},
					},
				},
			},
		},
	}
	for _, fn := range extra {
		fn(obj)
	}
	return obj
}

func TestIdenticalObjectsProduceEmptyPatch(t *testing.T) {
	got, err := diff.Between(deployment("nginx:1.27"), deployment("nginx:1.27"))
	if err != nil {
		t.Fatalf("Between() Fehler: %v", err)
	}
	if len(got.Patch) != 0 {
		t.Errorf("Patch ist nicht leer: %s", got.Patch)
	}
	if len(got.ChangedPaths) != 0 {
		t.Errorf("ChangedPaths ist nicht leer: %v", got.ChangedPaths)
	}
	if !got.Empty() {
		t.Error("Empty() meldet false bei identischen Objekten")
	}
}

func TestChangedImageYieldsExactlyOnePath(t *testing.T) {
	got, err := diff.Between(deployment("nginx:1.27"), deployment("nginx:1.27-alpine"))
	if err != nil {
		t.Fatalf("Between() Fehler: %v", err)
	}
	want := "/spec/template/spec/containers/0/image"
	if len(got.ChangedPaths) != 1 || got.ChangedPaths[0] != want {
		t.Errorf("ChangedPaths = %v, want [%s]", got.ChangedPaths, want)
	}
	if got.Empty() {
		t.Error("Empty() meldet true trotz Aenderung")
	}
}

func TestAddedLabelProducesAddOperation(t *testing.T) {
	withLabel := deployment("nginx:1.27", func(obj map[string]any) {
		meta, _ := obj["metadata"].(map[string]any)
		meta["labels"] = map[string]any{"team": "payments"}
	})
	got, err := diff.Between(deployment("nginx:1.27"), withLabel)
	if err != nil {
		t.Fatalf("Between() Fehler: %v", err)
	}
	if len(got.ChangedPaths) != 1 || got.ChangedPaths[0] != "/metadata/labels" {
		t.Errorf("ChangedPaths = %v, want [/metadata/labels]", got.ChangedPaths)
	}
	if !containsOp(string(got.Patch), "add") {
		t.Errorf("Patch enthaelt keine add-Operation: %s", got.Patch)
	}
}

func TestRemovedContainerProducesRemoveOperation(t *testing.T) {
	two := deployment("nginx:1.27", func(obj map[string]any) {
		spec, _ := obj["spec"].(map[string]any)
		tmpl, _ := spec["template"].(map[string]any)
		tspec, _ := tmpl["spec"].(map[string]any)
		tspec["containers"] = []any{
			map[string]any{"name": "nginx", "image": "nginx:1.27"},
			map[string]any{"name": "sidecar", "image": "busybox:1.36"},
		}
	})
	got, err := diff.Between(two, deployment("nginx:1.27"))
	if err != nil {
		t.Fatalf("Between() Fehler: %v", err)
	}
	if !containsOp(string(got.Patch), "remove") {
		t.Errorf("Patch enthaelt keine remove-Operation: %s", got.Patch)
	}
	if len(got.ChangedPaths) == 0 {
		t.Error("ChangedPaths ist leer trotz entferntem Container")
	}
}

// Doppelte Pfade werden zusammengefasst, sonst blaeht ein Listen-Rewrite die Spalte auf.
func TestChangedPathsAreUniqueAndSorted(t *testing.T) {
	before := deployment("nginx:1.27")
	after := deployment("nginx:1.28", func(obj map[string]any) {
		meta, _ := obj["metadata"].(map[string]any)
		meta["labels"] = map[string]any{"a": "1"}
	})
	got, err := diff.Between(before, after)
	if err != nil {
		t.Fatalf("Between() Fehler: %v", err)
	}
	seen := map[string]bool{}
	for i, p := range got.ChangedPaths {
		if seen[p] {
			t.Errorf("Pfad %q kommt doppelt vor", p)
		}
		seen[p] = true
		if i > 0 && got.ChangedPaths[i-1] > p {
			t.Errorf("ChangedPaths nicht sortiert: %v", got.ChangedPaths)
		}
	}
}

func containsOp(patch, op string) bool {
	return len(patch) > 0 && (indexOf(patch, `"op":"`+op+`"`) >= 0 || indexOf(patch, `"op": "`+op+`"`) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
