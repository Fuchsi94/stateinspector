// Package extract zieht die Felder aus einem Objekt, die als eigene Spalten
// abfragbar sein sollen.
package extract

// Images liefert alle Images des Pod-Templates: erst die Container in
// Manifest-Reihenfolge, dann die initContainers. Doppelte Images erscheinen
// einmal, damit die Spalte nicht durch Sidecars mit gleichem Image aufblaeht.
// Objekte ohne Pod-Template liefern nichts und sind kein Fehler.
func Images(obj map[string]any) []string {
	spec := nestedMap(obj, "spec", "template", "spec")
	if spec == nil {
		return nil
	}

	var images []string
	seen := map[string]struct{}{}
	for _, field := range []string{"containers", "initContainers"} {
		list, ok := spec[field].([]any)
		if !ok {
			continue
		}
		for _, entry := range list {
			container, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			image, ok := container["image"].(string)
			if !ok || image == "" {
				continue
			}
			if _, dup := seen[image]; dup {
				continue
			}
			seen[image] = struct{}{}
			images = append(images, image)
		}
	}
	return images
}

func nestedMap(obj map[string]any, path ...string) map[string]any {
	current := obj
	for _, key := range path {
		next, ok := current[key].(map[string]any)
		if !ok {
			return nil
		}
		current = next
	}
	return current
}
