package extract

import "fmt"

// Owner liefert die erste ownerReference mit controller: true als "Kind/Name".
// Ohne solche Referenz ist das Ergebnis leer; der Store schreibt dann NULL.
func Owner(obj map[string]any) string {
	metadata, ok := obj["metadata"].(map[string]any)
	if !ok {
		return ""
	}
	refs, ok := metadata["ownerReferences"].([]any)
	if !ok {
		return ""
	}
	for _, entry := range refs {
		ref, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if controller, _ := ref["controller"].(bool); !controller {
			continue
		}
		kind, _ := ref["kind"].(string)
		name, _ := ref["name"].(string)
		if kind == "" || name == "" {
			continue
		}
		return fmt.Sprintf("%s/%s", kind, name)
	}
	return ""
}
