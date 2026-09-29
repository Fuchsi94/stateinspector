package extract_test

import (
	"testing"

	"github.com/Fuchsi94/stateinspector/internal/extract"
)

func podTemplate(containers, initContainers []any) map[string]any {
	spec := map[string]any{}
	if containers != nil {
		spec["containers"] = containers
	}
	if initContainers != nil {
		spec["initContainers"] = initContainers
	}
	return map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": "web", "namespace": "demo"},
		"spec":       map[string]any{"template": map[string]any{"spec": spec}},
	}
}

func container(name, image string) any {
	return map[string]any{"name": name, "image": image}
}

func TestImagesFromContainersAndInitContainers(t *testing.T) {
	obj := podTemplate(
		[]any{container("nginx", "nginx:1.27")},
		[]any{container("wait", "busybox:1.36")},
	)
	got := extract.Images(obj)
	want := []string{"nginx:1.27", "busybox:1.36"}

	if len(got) != len(want) {
		t.Fatalf("Images() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Images()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// Ein Service hat kein Pod-Template: leeres Ergebnis, kein Fehler.
func TestImagesWithoutPodTemplate(t *testing.T) {
	svc := map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata":   map[string]any{"name": "web", "namespace": "demo"},
		"spec":       map[string]any{"clusterIP": "10.0.0.1"},
	}
	if got := extract.Images(svc); len(got) != 0 {
		t.Errorf("Images() = %v, want leer", got)
	}
}

func TestImagesSkipsEntriesWithoutImage(t *testing.T) {
	obj := podTemplate([]any{
		container("nginx", "nginx:1.27"),
		map[string]any{"name": "kaputt"},
		"kein container",
	}, nil)

	got := extract.Images(obj)
	if len(got) != 1 || got[0] != "nginx:1.27" {
		t.Errorf("Images() = %v, want [nginx:1.27]", got)
	}
}

// Zwei Container mit demselben Image ergeben einen Eintrag, nicht zwei.
func TestImagesAreDeduplicated(t *testing.T) {
	obj := podTemplate([]any{
		container("a", "nginx:1.27"),
		container("b", "nginx:1.27"),
	}, nil)

	got := extract.Images(obj)
	if len(got) != 1 {
		t.Errorf("Images() = %v, want einen Eintrag", got)
	}
}

func TestOwnerReturnsControllerReference(t *testing.T) {
	obj := map[string]any{
		"metadata": map[string]any{
			"name": "web-abc123",
			"ownerReferences": []any{
				map[string]any{"kind": "Deployment", "name": "other", "controller": false},
				map[string]any{"kind": "ReplicaSet", "name": "web-5d9f", "controller": true},
			},
		},
	}
	if got := extract.Owner(obj); got != "ReplicaSet/web-5d9f" {
		t.Errorf("Owner() = %q, want ReplicaSet/web-5d9f", got)
	}
}

func TestOwnerEmptyWithoutControllerReference(t *testing.T) {
	tests := map[string]map[string]any{
		"gar keine Referenzen": {"metadata": map[string]any{"name": "web"}},
		"leere Liste":          {"metadata": map[string]any{"ownerReferences": []any{}}},
		"keine mit controller": {"metadata": map[string]any{"ownerReferences": []any{
			map[string]any{"kind": "Deployment", "name": "web", "controller": false},
		}}},
	}
	for name, obj := range tests {
		t.Run(name, func(t *testing.T) {
			if got := extract.Owner(obj); got != "" {
				t.Errorf("Owner() = %q, want leer", got)
			}
		})
	}
}
