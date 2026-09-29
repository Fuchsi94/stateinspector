package mcp

import (
	"context"
	"fmt"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Fuchsi94/stateinspector/internal/diff"
	"github.com/Fuchsi94/stateinspector/internal/store"
)

// Zeitstempel gehen mit Sekundenbruchteilen raus. Ein Modell fuettert einen
// zurueckgegebenen observed_at naturgemaess wieder in get_resource oder
// diff_resource; auf Sekunden gerundet schneidet "observed_at <= at" genau die
// Version weg, nach der gefragt wurde.

// maxResults kappt jede Antwort. Ohne harte Grenze kann eine einzige Abfrage
// das Kontextfenster des fragenden Modells fuellen (R18).
const maxResults = 500

// Backend ist der Ausschnitt des Stores, den die Tools lesen.
type Backend interface {
	ListChanges(ctx context.Context, f store.ChangeFilter) ([]store.ChangeSummary, error)
	ObjectAt(ctx context.Context, cluster, kind, namespace, name string, at time.Time) (map[string]any, time.Time, bool, error)
	ListResources(ctx context.Context, cluster, kind, namespace string, includeDeleted bool, limit int) ([]store.ResourceSummary, error)
	CurrentWorkloads(ctx context.Context, cluster, namespace, nameContains string, limit int) ([]store.ResourceSummary, error)
}

// ChangeView ist ein Eintrag in list_changes.
type ChangeView struct {
	ObservedAt   string   `json:"observed_at"`
	ChangeType   string   `json:"change_type"`
	Offline      bool     `json:"offline"`
	Kind         string   `json:"kind"`
	Namespace    string   `json:"namespace"`
	Name         string   `json:"name"`
	ChangedPaths []string `json:"changed_paths"`
	Images       []string `json:"images"`
}

// ResourceView beschreibt ein beobachtetes Objekt.
type ResourceView struct {
	Kind        string   `json:"kind"`
	Namespace   string   `json:"namespace"`
	Name        string   `json:"name"`
	Images      []string `json:"images,omitempty"`
	LastChanged string   `json:"last_changed"`
	DeletedAt   string   `json:"deleted_at,omitempty"`
}

type listChangesIn struct {
	Since     string `json:"since" jsonschema:"Start des Zeitfensters. RFC 3339 (2026-09-29T10:00:00Z) oder relative Dauer rueckwaerts (30m, 24h, 7d)."`
	Until     string `json:"until,omitempty" jsonschema:"Ende des Zeitfensters, gleiche Schreibweise. Weglassen bedeutet jetzt."`
	Namespace string `json:"namespace,omitempty" jsonschema:"Nur dieser Namespace."`
	Kind      string `json:"kind,omitempty" jsonschema:"Nur diese Art, z.B. Deployment, Service, Ingress, ConfigMap."`
	Name      string `json:"name,omitempty" jsonschema:"Nur dieses Objekt (exakter Name)."`
	Limit     int    `json:"limit,omitempty" jsonschema:"Hoechstzahl Eintraege, Standard 50, Maximum 500."`
}

type listChangesOut struct {
	Changes   []ChangeView `json:"changes"`
	Truncated bool         `json:"truncated"`
	Note      string       `json:"note,omitempty"`
}

type getResourceIn struct {
	Kind      string `json:"kind" jsonschema:"Art des Objekts, z.B. Deployment."`
	Namespace string `json:"namespace" jsonschema:"Namespace des Objekts."`
	Name      string `json:"name" jsonschema:"Name des Objekts."`
	At        string `json:"at,omitempty" jsonschema:"Zeitpunkt. RFC 3339 oder relative Dauer rueckwaerts wie 7d. Weglassen bedeutet jetzt."`
}

type getResourceOut struct {
	Found      bool           `json:"found"`
	ObservedAt string         `json:"observed_at,omitempty"`
	Object     map[string]any `json:"object,omitempty"`
	Note       string         `json:"note,omitempty"`
}

type diffResourceIn struct {
	Kind      string `json:"kind" jsonschema:"Art des Objekts."`
	Namespace string `json:"namespace" jsonschema:"Namespace des Objekts."`
	Name      string `json:"name" jsonschema:"Name des Objekts."`
	From      string `json:"from" jsonschema:"Fruehere Zeitpunkt. RFC 3339 oder relative Dauer rueckwaerts wie 24h."`
	To        string `json:"to,omitempty" jsonschema:"Spaeterer Zeitpunkt, Standard jetzt."`
}

type diffResourceOut struct {
	Found          bool     `json:"found"`
	FromObservedAt string   `json:"from_observed_at,omitempty"`
	ToObservedAt   string   `json:"to_observed_at,omitempty"`
	Patch          any      `json:"patch,omitempty"`
	ChangedPaths   []string `json:"changed_paths,omitempty"`
	Note           string   `json:"note,omitempty"`
}

type getVersionsIn struct {
	Namespace    string `json:"namespace,omitempty" jsonschema:"Nur dieser Namespace."`
	NameContains string `json:"name_contains,omitempty" jsonschema:"Nur Objekte, deren Name diesen Text enthaelt."`
}

type getVersionsOut struct {
	Workloads []ResourceView `json:"workloads"`
	Truncated bool           `json:"truncated"`
	Note      string         `json:"note,omitempty"`
}

type listResourcesIn struct {
	Kind           string `json:"kind,omitempty" jsonschema:"Nur diese Art."`
	Namespace      string `json:"namespace,omitempty" jsonschema:"Nur dieser Namespace."`
	IncludeDeleted bool   `json:"include_deleted,omitempty" jsonschema:"Geloeschte Objekte mit auflisten. Standard false."`
}

type listResourcesOut struct {
	Resources []ResourceView `json:"resources"`
	Truncated bool           `json:"truncated"`
	Note      string         `json:"note,omitempty"`
}

// register haengt alle fuenf Tools an den Server. Die Beschreibungen sagen,
// wann ein Modell welches Werkzeug nimmt - ohne das raet es.
func (s *Server) register(srv *mcpsdk.Server) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "list_changes",
		Description: "Listet Aenderungen an Workloads in einem Zeitfenster, neueste zuerst. " +
			"Nimm dieses Tool fuer Fragen wie 'was hat sich seit gestern geaendert'. " +
			"Liefert bewusst nicht das vollstaendige Objekt, sondern nur, welche Pfade sich " +
			"geaendert haben - fuer das Objekt selbst nimm get_resource, fuer den genauen " +
			"Unterschied diff_resource.",
	}, s.listChanges)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "get_resource",
		Description: "Liefert ein Objekt so, wie es zu einem Zeitpunkt aussah, plus den Zeitstempel " +
			"der gelieferten Version. Nimm dieses Tool fuer 'wie sah X letzten Montag aus'. " +
			"Ohne 'at' bekommst du den aktuellen Stand.",
	}, s.getResource)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "diff_resource",
		Description: "Liefert den Unterschied eines Objekts zwischen zwei Zeitpunkten als " +
			"RFC-6902-Patch plus Liste der geaenderten Pfade. Nimm dieses Tool fuer " +
			"'was genau wurde beim letzten Rollout geaendert'.",
	}, s.diffResource)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "get_versions",
		Description: "Listet aktuell laufende Workloads mit ihren Container-Images. " +
			"Nimm dieses Tool fuer 'welche Image-Version laeuft von Service Y'.",
	}, s.getVersions)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "list_resources",
		Description: "Uebersicht aller beobachteten Objekte mit Zeitpunkt der letzten Aenderung. " +
			"Nimm dieses Tool, um zu sehen, was ueberhaupt existiert, oder mit " +
			"include_deleted, um zu finden, was geloescht wurde.",
	}, s.listResources)
}

func (s *Server) listChanges(ctx context.Context, _ *mcpsdk.CallToolRequest, in listChangesIn) (*mcpsdk.CallToolResult, listChangesOut, error) {
	since, err := ParseTime(in.Since, s.now())
	if err != nil {
		return nil, listChangesOut{}, fmt.Errorf("since: %w", err)
	}
	until := s.now()
	if in.Until != "" {
		if until, err = ParseTime(in.Until, s.now()); err != nil {
			return nil, listChangesOut{}, fmt.Errorf("until: %w", err)
		}
	}

	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	capped := limit > maxResults
	if capped {
		limit = maxResults
	}

	// Ein Eintrag mehr abfragen, als geliefert wird: nur so ist bekannt, ob
	// abgeschnitten wurde, statt es zu behaupten.
	rows, err := s.backend.ListChanges(ctx, store.ChangeFilter{
		Cluster: s.cluster, Since: since, Until: until,
		Namespace: in.Namespace, Kind: in.Kind, Name: in.Name, Limit: limit + 1,
	})
	if err != nil {
		return nil, listChangesOut{}, err
	}

	out := listChangesOut{Changes: []ChangeView{}}
	out.Truncated = len(rows) > limit
	if out.Truncated {
		rows = rows[:limit]
		out.Note = fmt.Sprintf("Bei %d Eintraegen abgeschnitten; es gibt mehr. Grenze das Zeitfenster oder den Namespace ein.", limit)
	}
	for _, row := range rows {
		out.Changes = append(out.Changes, ChangeView{
			ObservedAt: row.ObservedAt.Format(time.RFC3339Nano), ChangeType: string(row.Type),
			Offline: row.Offline, Kind: row.Kind, Namespace: row.Namespace, Name: row.Name,
			ChangedPaths: row.ChangedPaths, Images: row.Images,
		})
	}
	return nil, out, nil
}

func (s *Server) getResource(ctx context.Context, _ *mcpsdk.CallToolRequest, in getResourceIn) (*mcpsdk.CallToolResult, getResourceOut, error) {
	at := s.now()
	if in.At != "" {
		parsed, err := ParseTime(in.At, s.now())
		if err != nil {
			return nil, getResourceOut{}, fmt.Errorf("at: %w", err)
		}
		at = parsed
	}

	object, observedAt, found, err := s.backend.ObjectAt(ctx, s.cluster, in.Kind, in.Namespace, in.Name, at)
	if err != nil {
		return nil, getResourceOut{}, err
	}
	if !found {
		// Kein Fehler: "existierte damals nicht" ist eine gueltige Antwort.
		return nil, getResourceOut{Note: fmt.Sprintf(
			"%s %s/%s existierte am %s nicht (oder wird nicht beobachtet).",
			in.Kind, in.Namespace, in.Name, at.Format(time.RFC3339Nano))}, nil
	}
	return nil, getResourceOut{
		Found: true, ObservedAt: observedAt.Format(time.RFC3339Nano), Object: object,
	}, nil
}

func (s *Server) diffResource(ctx context.Context, _ *mcpsdk.CallToolRequest, in diffResourceIn) (*mcpsdk.CallToolResult, diffResourceOut, error) {
	from, err := ParseTime(in.From, s.now())
	if err != nil {
		return nil, diffResourceOut{}, fmt.Errorf("from: %w", err)
	}
	to := s.now()
	if in.To != "" {
		if to, err = ParseTime(in.To, s.now()); err != nil {
			return nil, diffResourceOut{}, fmt.Errorf("to: %w", err)
		}
	}

	before, beforeAt, beforeFound, err := s.backend.ObjectAt(ctx, s.cluster, in.Kind, in.Namespace, in.Name, from)
	if err != nil {
		return nil, diffResourceOut{}, err
	}
	after, afterAt, afterFound, err := s.backend.ObjectAt(ctx, s.cluster, in.Kind, in.Namespace, in.Name, to)
	if err != nil {
		return nil, diffResourceOut{}, err
	}
	if !beforeFound || !afterFound {
		return nil, diffResourceOut{Note: fmt.Sprintf(
			"%s %s/%s existierte zu mindestens einem der beiden Zeitpunkte nicht.",
			in.Kind, in.Namespace, in.Name)}, nil
	}

	result, err := diff.Between(before, after)
	if err != nil {
		return nil, diffResourceOut{}, err
	}
	out := diffResourceOut{
		Found:          true,
		FromObservedAt: beforeAt.Format(time.RFC3339Nano),
		ToObservedAt:   afterAt.Format(time.RFC3339Nano),
		ChangedPaths:   result.ChangedPaths,
	}
	if len(result.Patch) > 0 {
		out.Patch = result.Patch
	}
	if result.Empty() {
		out.Note = "Zwischen den beiden Zeitpunkten hat sich nichts geaendert."
	}
	return nil, out, nil
}

func (s *Server) getVersions(ctx context.Context, _ *mcpsdk.CallToolRequest, in getVersionsIn) (*mcpsdk.CallToolResult, getVersionsOut, error) {
	rows, err := s.backend.CurrentWorkloads(ctx, s.cluster, in.Namespace, in.NameContains, maxResults+1)
	if err != nil {
		return nil, getVersionsOut{}, err
	}
	out := getVersionsOut{Workloads: []ResourceView{}}
	rows, out.Truncated, out.Note = capRows(rows)
	for _, row := range rows {
		out.Workloads = append(out.Workloads, view(row))
	}
	return nil, out, nil
}

func (s *Server) listResources(ctx context.Context, _ *mcpsdk.CallToolRequest, in listResourcesIn) (*mcpsdk.CallToolResult, listResourcesOut, error) {
	rows, err := s.backend.ListResources(ctx, s.cluster, in.Kind, in.Namespace, in.IncludeDeleted, maxResults+1)
	if err != nil {
		return nil, listResourcesOut{}, err
	}
	out := listResourcesOut{Resources: []ResourceView{}}
	rows, out.Truncated, out.Note = capRows(rows)
	for _, row := range rows {
		out.Resources = append(out.Resources, view(row))
	}
	return nil, out, nil
}

// capRows schneidet auf maxResults und meldet, dass abgeschnitten wurde.
func capRows(rows []store.ResourceSummary) ([]store.ResourceSummary, bool, string) {
	if len(rows) <= maxResults {
		return rows, false, ""
	}
	return rows[:maxResults], true, fmt.Sprintf(
		"Bei %d Eintraegen abgeschnitten; es gibt mehr. Grenze Namespace oder Art ein.", maxResults)
}

func view(row store.ResourceSummary) ResourceView {
	out := ResourceView{
		Kind: row.Kind, Namespace: row.Namespace, Name: row.Name,
		Images: row.Images, LastChanged: row.LastChanged.Format(time.RFC3339Nano),
	}
	if row.DeletedAt != nil {
		out.DeletedAt = row.DeletedAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}
