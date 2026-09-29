package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSplitList(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"leer", "", nil},
		{"nur Trennzeichen", " , , ", nil},
		{"einzeln", "demo", []string{"demo"}},
		{"mehrere mit Leerzeichen", "demo, other ,third", []string{"demo", "other", "third"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitList(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("splitList(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("splitList(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// R2: ohne --namespaces gilt der ganze Cluster ausser den drei Systemnamespaces.
func TestNamespaceFilterDefaultExcludesSystemNamespaces(t *testing.T) {
	f := newNamespaceFilter(nil, splitList(defaultExcludeNamespaces))

	for _, ns := range []string{"kube-system", "kube-public", "kube-node-lease"} {
		if f.watches(ns) {
			t.Errorf("Namespace %q wird beobachtet, sollte per Default ausgeschlossen sein", ns)
		}
	}
	for _, ns := range []string{"demo", "default", "stateinspector"} {
		if !f.watches(ns) {
			t.Errorf("Namespace %q wird nicht beobachtet, sollte es aber", ns)
		}
	}
	if f.cacheNamespaces() != nil {
		t.Error("ohne Include-Liste darf der Cache nicht auf Namespaces eingeschraenkt werden")
	}
}

func TestNamespaceFilterIncludeList(t *testing.T) {
	f := newNamespaceFilter(splitList("demo,team-a"), splitList(defaultExcludeNamespaces))

	if !f.watches("demo") {
		t.Error("demo steht auf der Include-Liste und muss beobachtet werden")
	}
	if f.watches("other") {
		t.Error("other steht nicht auf der Include-Liste und darf nicht beobachtet werden")
	}
	got := f.cacheNamespaces()
	if len(got) != 2 {
		t.Fatalf("cacheNamespaces() hat %d Eintraege, want 2: %v", len(got), got)
	}
	if _, ok := got["demo"]; !ok {
		t.Error("cacheNamespaces() enthaelt demo nicht")
	}
}

// Ausschluss schlaegt Einschluss - sonst koennte ein Tippfehler kube-system oeffnen.
func TestNamespaceFilterExcludeBeatsInclude(t *testing.T) {
	f := newNamespaceFilter(splitList("demo,kube-system"), splitList(defaultExcludeNamespaces))

	if f.watches("kube-system") {
		t.Error("kube-system steht auf beiden Listen und muss ausgeschlossen bleiben")
	}
	if _, ok := f.cacheNamespaces()["kube-system"]; ok {
		t.Error("kube-system darf nicht in den Cache-Namespaces auftauchen")
	}
}

// R21/Startbedingung: fehlendes DATABASE_URL bricht ab und nennt die Variable.
func TestRequireDatabaseURL(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    string
		wantErr bool
	}{
		{"gesetzt", map[string]string{"DATABASE_URL": "postgres://x/y"}, "postgres://x/y", false},
		{"fehlt", map[string]string{}, "", true},
		{"leer", map[string]string{"DATABASE_URL": "   "}, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := requireDatabaseURL(func(k string) string { return tc.env[k] })
			if tc.wantErr {
				if err == nil {
					t.Fatal("requireDatabaseURL() = nil error, want error")
				}
				if !strings.Contains(err.Error(), "DATABASE_URL") {
					t.Errorf("Fehlermeldung nennt DATABASE_URL nicht: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("requireDatabaseURL() unerwarteter Fehler: %v", err)
			}
			if got != tc.want {
				t.Errorf("requireDatabaseURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

// R21: Readiness wird erst gruen, wenn jede Vorbedingung erfuellt ist.
func TestReadyGate(t *testing.T) {
	g := newReadyGate("migrations", "database", "cache")

	if err := g.check(httptest.NewRequest("GET", "/readyz", nil)); err == nil {
		t.Fatal("frisches Gate meldet ready, darf es nicht")
	}

	g.done("migrations")
	g.done("database")
	err := g.check(httptest.NewRequest("GET", "/readyz", nil))
	if err == nil {
		t.Fatal("Gate mit offener Vorbedingung meldet ready, darf es nicht")
	}
	if !strings.Contains(err.Error(), "cache") {
		t.Errorf("Fehler nennt die offene Vorbedingung nicht: %v", err)
	}

	g.done("cache")
	if err := g.check(httptest.NewRequest("GET", "/readyz", nil)); err != nil {
		t.Fatalf("Gate mit allen Vorbedingungen meldet nicht ready: %v", err)
	}
}

// Ein unbekannter Name darf das Gate nicht unbemerkt oeffnen.
func TestReadyGateIgnoresUnknownCondition(t *testing.T) {
	g := newReadyGate("cache")
	g.done("tippfehler")

	if err := g.check(httptest.NewRequest("GET", "/readyz", nil)); err == nil {
		t.Fatal("Gate wurde durch einen unbekannten Namen geoeffnet")
	}
}
