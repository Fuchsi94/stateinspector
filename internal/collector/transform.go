package collector

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/Fuchsi94/stateinspector/internal/normalize"
)

// StripConfigMapValues ist die TransformFunc des Caches. Sie laeuft, bevor ein
// Objekt im Informer-Store landet, und ersetzt ConfigMap-Werte durch
// Schluesselliste und Hash.
//
// Im Handler zu strippen waere zu spaet: dann laegen die Werte bereits im
// Arbeitsspeicher des Prozesses, und CLAUDE.md verlangt, dass sie nirgends
// auftauchen (R9, KTD7).
func StripConfigMapValues(in any) (any, error) {
	u, ok := in.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("cache transform got %T, want *unstructured.Unstructured", in)
	}
	if u.GetKind() != "ConfigMap" {
		return u, nil
	}
	if err := normalize.StripConfigMapData(u.Object); err != nil {
		return nil, fmt.Errorf("strip configmap data: %w", err)
	}
	return u, nil
}
