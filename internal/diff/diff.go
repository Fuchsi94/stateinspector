// Package diff beschreibt den Unterschied zwischen zwei normalisierten
// Objektversionen als RFC-6902-Patch plus Liste der geaenderten Pfade.
package diff

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/wI2L/jsondiff"
)

// Result haelt den Patch und die daraus abgeleiteten Pfade. Die Pfadliste
// existiert separat, damit Abfragen wie "was hat sich am Image geaendert"
// ohne JSON-Parsing des Patches auskommen.
type Result struct {
	Patch        json.RawMessage
	ChangedPaths []string
}

// Empty meldet, ob sich ueberhaupt etwas geaendert hat.
func (r Result) Empty() bool { return len(r.ChangedPaths) == 0 }

// Between vergleicht zwei normalisierte Objekte.
func Between(before, after map[string]any) (Result, error) {
	patch, err := jsondiff.Compare(before, after)
	if err != nil {
		return Result{}, fmt.Errorf("compare objects: %w", err)
	}
	if len(patch) == 0 {
		return Result{}, nil
	}

	// Ein Listen-Rewrite erzeugt mehrere Operationen auf demselben Pfad;
	// dedupliziert bleibt die Spalte lesbar.
	unique := make(map[string]struct{}, len(patch))
	for _, op := range patch {
		unique[op.Path] = struct{}{}
	}
	paths := make([]string, 0, len(unique))
	for path := range unique {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	encoded, err := json.Marshal(patch)
	if err != nil {
		return Result{}, fmt.Errorf("encode patch: %w", err)
	}
	return Result{Patch: encoded, ChangedPaths: paths}, nil
}
