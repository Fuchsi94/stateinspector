package normalize

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Hash bildet den SHA-256 ueber die kanonische JSON-Darstellung.
// encoding/json sortiert die Schluessel einer map[string]any, damit haengt der
// Hash nicht an der Reihenfolge im eingehenden Objekt.
func Hash(obj map[string]any) (string, error) {
	canonical, err := json.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("canonicalize object: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}
