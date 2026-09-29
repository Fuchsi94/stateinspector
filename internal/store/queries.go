package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Snapshot ist der zuletzt gespeicherte Stand eines Objekts.
type Snapshot struct {
	Hash   string
	Object map[string]any
}

// Current liefert den aktuellen Stand zu einer UID. Der Collector fragt das
// nur, wenn sein In-Memory-Cache die UID nicht kennt.
func (s *Store) Current(ctx context.Context, uid string) (Snapshot, bool, error) {
	var (
		hash string
		raw  []byte
	)
	err := s.pool.QueryRow(ctx,
		`SELECT hash, object FROM resources WHERE uid = $1::uuid AND deleted_at IS NULL`,
		uid).Scan(&hash, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("load current resource: %w", err)
	}

	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return Snapshot{}, false, fmt.Errorf("decode current object: %w", err)
	}
	return Snapshot{Hash: hash, Object: object}, true, nil
}

// LiveResource ist ein noch nicht als geloescht markiertes Objekt, mit allem,
// was fuer einen vollwertigen deleted-Eintrag noetig ist.
type LiveResource struct {
	UID        string
	APIGroup   string
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
	Hash       string
	Object     map[string]any
	Images     []string
}

// LiveResources liefert alles, was der Store fuer lebendig haelt. Der Resync
// nach dem Cache-Sync gleicht damit ab, was waehrend eines Ausfalls
// verschwunden ist (R13).
func (s *Store) LiveResources(ctx context.Context, cluster string) ([]LiveResource, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT uid::text, api_group, api_version, kind, namespace, name, hash, object, images
		FROM resources
		WHERE cluster = $1 AND deleted_at IS NULL`, cluster)
	if err != nil {
		return nil, fmt.Errorf("list live resources: %w", err)
	}
	defer rows.Close()

	var out []LiveResource
	for rows.Next() {
		var (
			item LiveResource
			raw  []byte
		)
		if err := rows.Scan(&item.UID, &item.APIGroup, &item.APIVersion, &item.Kind,
			&item.Namespace, &item.Name, &item.Hash, &raw, &item.Images); err != nil {
			return nil, fmt.Errorf("scan live resource: %w", err)
		}
		if err := json.Unmarshal(raw, &item.Object); err != nil {
			return nil, fmt.Errorf("decode live resource %s: %w", item.UID, err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate live resources: %w", err)
	}
	return out, nil
}

// WriteChanges schreibt einen Stapel in einer Transaktion: jede Version nach
// changes, der abgeleitete Stand nach resources. Entweder alles oder nichts,
// damit resources nie einen Stand zeigt, zu dem die Version fehlt.
func (s *Store) WriteChanges(ctx context.Context, changes []Change) error {
	if len(changes) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, c := range changes {
		object, err := json.Marshal(c.Object)
		if err != nil {
			return fmt.Errorf("encode object for %s/%s: %w", c.Namespace, c.Name, err)
		}
		var patch any
		if len(c.Patch) > 0 {
			patch = []byte(c.Patch)
		}
		var owner any
		if c.Owner != "" {
			owner = c.Owner
		}
		images := c.Images
		if images == nil {
			images = []string{}
		}
		paths := c.ChangedPaths
		if paths == nil {
			paths = []string{}
		}

		// actor bleibt bewusst unbesetzt: die Audit-Log-Anbindung ist ein Nicht-Ziel.
		if _, err := tx.Exec(ctx, `
			INSERT INTO changes (uid, cluster, api_group, kind, namespace, name,
			                     change_type, offline, observed_at, hash, object,
			                     patch, changed_paths, images, owner)
			VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
			c.UID, c.Cluster, c.APIGroup, c.Kind, c.Namespace, c.Name,
			string(c.Type), c.Offline, c.ObservedAt.UTC(), c.Hash, object,
			patch, paths, images, owner,
		); err != nil {
			return fmt.Errorf("insert change for %s/%s: %w", c.Namespace, c.Name, err)
		}

		if err := upsertResource(ctx, tx, c, object, images); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit changes: %w", err)
	}
	return nil
}

func upsertResource(ctx context.Context, tx pgx.Tx, c Change, object []byte, images []string) error {
	var deletedAt any
	if c.Type == Deleted {
		deletedAt = c.ObservedAt.UTC()
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO resources (uid, cluster, api_group, api_version, kind, namespace, name,
		                       hash, object, images, first_seen, last_changed, deleted_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11, $12)
		ON CONFLICT (uid) DO UPDATE SET
			hash         = EXCLUDED.hash,
			object       = EXCLUDED.object,
			images       = EXCLUDED.images,
			last_changed = EXCLUDED.last_changed,
			deleted_at   = EXCLUDED.deleted_at`,
		c.UID, c.Cluster, c.APIGroup, c.APIVersion, c.Kind, c.Namespace, c.Name,
		c.Hash, object, images, c.ObservedAt.UTC(), deletedAt,
	); err != nil {
		return fmt.Errorf("upsert resource for %s/%s: %w", c.Namespace, c.Name, err)
	}
	return nil
}

// ObjectAt liefert den Zustand zu einem Zeitpunkt: die letzte Version mit
// observed_at <= at. War diese Version eine Loeschung, existierte das Objekt
// zu diesem Zeitpunkt nicht mehr.
func (s *Store) ObjectAt(ctx context.Context, cluster, kind, namespace, name string, at time.Time) (map[string]any, time.Time, bool, error) {
	var (
		raw        []byte
		observedAt time.Time
		changeType string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT object, observed_at, change_type
		FROM changes
		WHERE cluster = $1 AND kind = $2 AND namespace = $3 AND name = $4 AND observed_at <= $5
		ORDER BY observed_at DESC, id DESC
		LIMIT 1`,
		cluster, kind, namespace, name, at.UTC()).Scan(&raw, &observedAt, &changeType)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, fmt.Errorf("load object at timestamp: %w", err)
	}
	if ChangeType(changeType) == Deleted {
		return nil, time.Time{}, false, nil
	}

	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, time.Time{}, false, fmt.Errorf("decode object at timestamp: %w", err)
	}
	return object, observedAt.UTC(), true, nil
}

// ChangeFilter schraenkt list_changes ein. Leere Felder filtern nicht.
type ChangeFilter struct {
	Cluster   string
	Since     time.Time
	Until     time.Time
	Namespace string
	Kind      string
	Name      string
	Limit     int
}

// ChangeSummary ist die Listenansicht: ohne das vollstaendige Objekt, damit
// eine Antwort nicht das Kontextfenster eines Modells sprengt (R14).
type ChangeSummary struct {
	ObservedAt   time.Time
	Type         ChangeType
	Offline      bool
	Kind         string
	Namespace    string
	Name         string
	ChangedPaths []string
	Images       []string
}

// ListChanges liefert Aenderungen im Zeitfenster, neueste zuerst.
func (s *Store) ListChanges(ctx context.Context, f ChangeFilter) ([]ChangeSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT observed_at, change_type, offline, kind, namespace, name, changed_paths, images
		FROM changes
		WHERE cluster = $1
		  AND observed_at >= $2
		  AND observed_at <= $3
		  AND ($4 = '' OR namespace = $4)
		  AND ($5 = '' OR kind = $5)
		  AND ($6 = '' OR name = $6)
		ORDER BY observed_at DESC, id DESC
		LIMIT $7`,
		f.Cluster, f.Since.UTC(), f.Until.UTC(), f.Namespace, f.Kind, f.Name, f.Limit)
	if err != nil {
		return nil, fmt.Errorf("list changes: %w", err)
	}
	defer rows.Close()

	var out []ChangeSummary
	for rows.Next() {
		var (
			item       ChangeSummary
			changeType string
		)
		if err := rows.Scan(&item.ObservedAt, &changeType, &item.Offline, &item.Kind,
			&item.Namespace, &item.Name, &item.ChangedPaths, &item.Images); err != nil {
			return nil, fmt.Errorf("scan change: %w", err)
		}
		item.Type = ChangeType(changeType)
		item.ObservedAt = item.ObservedAt.UTC()
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate changes: %w", err)
	}
	return out, nil
}

// ResourceSummary ist die Uebersicht ueber beobachtete Objekte (R17).
type ResourceSummary struct {
	Kind        string
	Namespace   string
	Name        string
	Images      []string
	LastChanged time.Time
	DeletedAt   *time.Time
}

// ListResources liefert beobachtete Objekte, optional inklusive geloeschter.
func (s *Store) ListResources(ctx context.Context, cluster, kind, namespace string, includeDeleted bool, limit int) ([]ResourceSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT kind, namespace, name, images, last_changed, deleted_at
		FROM resources
		WHERE cluster = $1
		  AND ($2 = '' OR kind = $2)
		  AND ($3 = '' OR namespace = $3)
		  AND ($4 OR deleted_at IS NULL)
		ORDER BY namespace, kind, name
		LIMIT $5`,
		cluster, kind, namespace, includeDeleted, limit)
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}
	defer rows.Close()

	var out []ResourceSummary
	for rows.Next() {
		var item ResourceSummary
		if err := rows.Scan(&item.Kind, &item.Namespace, &item.Name, &item.Images,
			&item.LastChanged, &item.DeletedAt); err != nil {
			return nil, fmt.Errorf("scan resource: %w", err)
		}
		item.LastChanged = item.LastChanged.UTC()
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate resources: %w", err)
	}
	return out, nil
}

// CurrentWorkloads liefert lebende Objekte mit ihren Images; das beantwortet
// "welche Version laeuft von X" (R17).
func (s *Store) CurrentWorkloads(ctx context.Context, cluster, namespace, nameContains string, limit int) ([]ResourceSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT kind, namespace, name, images, last_changed, deleted_at
		FROM resources
		WHERE cluster = $1
		  AND deleted_at IS NULL
		  AND cardinality(images) > 0
		  AND ($2 = '' OR namespace = $2)
		  AND ($3 = '' OR name ILIKE '%' || $3 || '%')
		ORDER BY namespace, kind, name
		LIMIT $4`,
		cluster, namespace, nameContains, limit)
	if err != nil {
		return nil, fmt.Errorf("list current workloads: %w", err)
	}
	defer rows.Close()

	var out []ResourceSummary
	for rows.Next() {
		var item ResourceSummary
		if err := rows.Scan(&item.Kind, &item.Namespace, &item.Name, &item.Images,
			&item.LastChanged, &item.DeletedAt); err != nil {
			return nil, fmt.Errorf("scan workload: %w", err)
		}
		item.LastChanged = item.LastChanged.UTC()
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workloads: %w", err)
	}
	return out, nil
}
