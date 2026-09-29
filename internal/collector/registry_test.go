package collector_test

import (
	"os"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/Fuchsi94/stateinspector/internal/collector"
)

const rolePath = "../../config/base/rbac/role.yaml"

type clusterRole struct {
	Rules []struct {
		APIGroups []string `json:"apiGroups"`
		Resources []string `json:"resources"`
		Verbs     []string `json:"verbs"`
	} `json:"rules"`
}

func loadRole(t *testing.T) clusterRole {
	t.Helper()
	raw, err := os.ReadFile(rolePath)
	if err != nil {
		t.Fatalf("%s lesen: %v", rolePath, err)
	}
	var role clusterRole
	if err := yaml.Unmarshal(raw, &role); err != nil {
		t.Fatalf("%s parsen: %v", rolePath, err)
	}
	if len(role.Rules) == 0 {
		t.Fatalf("%s enthaelt keine Regeln", rolePath)
	}
	return role
}

func TestWatchedGVKsAreWellFormed(t *testing.T) {
	if len(collector.Watched) == 0 {
		t.Fatal("Watched ist leer")
	}
	seen := map[string]bool{}
	for _, gvk := range collector.Watched {
		if gvk.Version == "" || gvk.Kind == "" {
			t.Errorf("unvollstaendige GVK: %+v", gvk)
		}
		if seen[gvk.String()] {
			t.Errorf("GVK doppelt registriert: %s", gvk)
		}
		seen[gvk.String()] = true
	}
}

// R19: nur lesen. Keine Wildcards, keine Secrets.
func TestRoleGrantsOnlyReadVerbs(t *testing.T) {
	allowed := map[string]bool{"get": true, "list": true, "watch": true}
	for _, rule := range loadRole(t).Rules {
		for _, verb := range rule.Verbs {
			if !allowed[verb] {
				t.Errorf("unerlaubtes Verb %q fuer %v", verb, rule.Resources)
			}
		}
		for _, group := range rule.APIGroups {
			if group == "*" {
				t.Error("Wildcard in apiGroups")
			}
		}
		for _, resource := range rule.Resources {
			if resource == "*" {
				t.Error("Wildcard in resources")
			}
			if strings.HasPrefix(resource, "secrets") {
				t.Errorf("Zugriff auf %q ist verboten", resource)
			}
		}
	}
}

// Der haeufigste Fehler beim Hinzufuegen einer Art: RBAC-Marker vergessen.
// Dann bekommt der Informer 403, der Cache synct nie und Readiness bleibt rot.
func TestEveryWatchedKindHasRBAC(t *testing.T) {
	granted := map[string]map[string]bool{}
	for _, rule := range loadRole(t).Rules {
		for _, group := range rule.APIGroups {
			if granted[group] == nil {
				granted[group] = map[string]bool{}
			}
			for _, resource := range rule.Resources {
				granted[group][resource] = true
			}
		}
	}

	for _, gvk := range collector.Watched {
		resource := pluralize(gvk.Kind)
		if !granted[gvk.Group][resource] {
			t.Errorf("kein RBAC fuer %s (Gruppe %q, Ressource %q) - Marker in registry.go vergessen?",
				gvk.Kind, gvk.Group, resource)
		}
	}
}

// pluralize bildet die Kubernetes-Pluralform: Ingress -> ingresses,
// Deployment -> deployments.
func pluralize(kind string) string {
	lower := strings.ToLower(kind)
	if strings.HasSuffix(lower, "s") {
		return lower + "es"
	}
	return lower + "s"
}
