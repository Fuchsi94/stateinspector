// Package normalize entfernt aus Kubernetes-Objekten alles, was sich ohne echte
// Aenderung bewegt, und macht sie damit vergleichbar und hashbar.
package normalize

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Object normalisiert eine Kopie des Objekts. Das Original bleibt unangetastet,
// damit der Informer-Cache nicht ueber die Normalisierung veraendert wird.
func Object(u *unstructured.Unstructured) (map[string]any, error) {
	obj := u.DeepCopy().Object

	for _, rule := range globalRules {
		if err := rule(obj); err != nil {
			return nil, fmt.Errorf("apply global rule: %w", err)
		}
	}
	for _, rule := range kindRules[u.GroupVersionKind().GroupKind()] {
		if err := rule(obj); err != nil {
			return nil, fmt.Errorf("apply kind rule: %w", err)
		}
	}

	pruned, _ := prune(obj)
	result, ok := pruned.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("normalized object is %T, want map", pruned)
	}
	return result, nil
}

// prune entfernt leere Maps, leere Listen und nil-Werte, damit {} und ein
// fehlendes Feld identisch hashen (R6). Bottom-up: wird ein inneres Feld leer,
// faellt der Elternknoten im selben Durchlauf mit.
func prune(value any) (any, bool) {
	switch typed := value.(type) {
	case nil:
		return nil, true
	case map[string]any:
		for key, inner := range typed {
			cleaned, empty := prune(inner)
			if empty {
				delete(typed, key)
				continue
			}
			typed[key] = cleaned
		}
		return typed, len(typed) == 0
	case []any:
		// Listenelemente bleiben erhalten, auch wenn eines leer wird: sonst
		// verschoeben sich Indizes und der Diff zeigt Aenderungen, die es nicht gab.
		for i, inner := range typed {
			cleaned, _ := prune(inner)
			typed[i] = cleaned
		}
		return typed, len(typed) == 0
	default:
		return typed, false
	}
}
