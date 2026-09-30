package normalize

import (
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Rule veraendert ein Objekt an Ort und Stelle. Regeln sind bewusst klein und
// einzeln benannt, damit eine neue Ressourcenart nur eine Zeile in der Tabelle
// unten kostet.
type Rule func(obj map[string]any) error

// globalRules entfernen Felder, die der API-Server verwaltet und die sich ohne
// echte Aenderung bewegen (R6, R7).
var globalRules = []Rule{
	removeField("status"),
	removeField("metadata", "managedFields"),
	removeField("metadata", "resourceVersion"),
	removeField("metadata", "generation"),
	removeField("metadata", "creationTimestamp"),
	removeField("metadata", "selfLink"),
	removeField("metadata", "uid"),
	// last-applied-configuration ist eine Kopie des Objekts, revision zaehlt
	// ReplicaSets. restartedAt bleibt bewusst stehen: das ist ein echter Rollout.
	removeAnnotations(
		"kubectl.kubernetes.io/last-applied-configuration",
		"deployment.kubernetes.io/revision",
	),
}

// kindRules ergaenzen die globalen Regeln je Ressourcenart.
var kindRules = map[schema.GroupKind][]Rule{
	{Group: "", Kind: "Service"}: {
		// clusterIP und clusterIPs bleiben stehen: sie sind stabil und
		// aussagekraeftig. Die IP-Familien setzt der API-Server je Cluster.
		removeField("spec", "ipFamilies"),
		removeField("spec", "ipFamilyPolicy"),
	},
	{Group: "", Kind: "ConfigMap"}: {
		summarizeData("data"),
		summarizeData("binaryData"),
	},
}

// removeField loescht ein verschachteltes Feld, falls es existiert.
func removeField(path ...string) Rule {
	return func(obj map[string]any) error {
		parent := obj
		for _, key := range path[:len(path)-1] {
			next, ok := parent[key].(map[string]any)
			if !ok {
				return nil
			}
			parent = next
		}
		delete(parent, path[len(path)-1])
		return nil
	}
}

// removeAnnotations entfernt einzelne Annotationen, ohne die uebrigen anzufassen.
func removeAnnotations(keys ...string) Rule {
	return func(obj map[string]any) error {
		metadata, ok := obj["metadata"].(map[string]any)
		if !ok {
			return nil
		}
		annotations, ok := metadata["annotations"].(map[string]any)
		if !ok {
			return nil
		}
		for _, key := range keys {
			delete(annotations, key)
		}
		return nil
	}
}

// summarizeData ersetzt ein Datenfeld durch Schluesselliste und Inhalts-Hash.
// Die Werte selbst verlassen diese Funktion nie - das ist die harte Regel aus
// CLAUDE.md und R8.
func summarizeData(field string) Rule {
	return func(obj map[string]any) error {
		data, ok := obj[field].(map[string]any)
		if !ok || len(data) == 0 {
			return nil
		}
		keys := make([]string, 0, len(data))
		for key := range data {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		sum, err := Hash(data)
		if err != nil {
			return fmt.Errorf("summarize %s: %w", field, err)
		}

		asAny := make([]any, len(keys))
		for i, key := range keys {
			asAny[i] = key
		}
		obj[field] = map[string]any{
			"keys":   asAny,
			"sha256": sum,
		}
		return nil
	}
}

// StripConfigMapData ersetzt data und binaryData durch Schluessel und Hash.
// Der Cache-Transform des Collectors nutzt dieselbe Funktion wie die
// Normalisierung, damit beide Pfade nicht auseinanderlaufen (R8, R9).
func StripConfigMapData(obj map[string]any) error {
	for _, field := range []string{"data", "binaryData"} {
		if err := summarizeData(field)(obj); err != nil {
			return err
		}
	}
	return nil
}
