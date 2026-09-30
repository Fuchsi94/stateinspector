// Package store haelt die Historie in Postgres: den aktuellen Stand je Objekt
// in resources, jede Version vollstaendig in changes.
package store

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ChangeType unterscheidet die drei Ereignisse, die eine Version erzeugen.
type ChangeType string

// Die drei erlaubten Werte; der CHECK-Constraint in der Migration spiegelt sie.
const (
	Created ChangeType = "created"
	Updated ChangeType = "updated"
	Deleted ChangeType = "deleted"
)

// Change ist eine einzelne Version eines Objekts.
type Change struct {
	UID          string
	Cluster      string
	APIGroup     string
	APIVersion   string
	Kind         string
	Namespace    string
	Name         string
	Type         ChangeType
	Offline      bool
	ObservedAt   time.Time
	Hash         string
	Object       map[string]any
	Patch        json.RawMessage
	ChangedPaths []string
	Images       []string
	Owner        string
}

// Store buendelt den Verbindungspool. Alle Zeitangaben laufen in UTC.
type Store struct {
	pool *pgxpool.Pool
}

// New oeffnet den Pool. Migrationen laufen bewusst separat ueber Migrate,
// damit die Readiness-Vorbedingungen einzeln quittiert werden koennen (R21).
func New(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close gibt den Verbindungspool frei.
func (s *Store) Close() { s.pool.Close() }

// Ping prueft die Erreichbarkeit der Datenbank.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	return nil
}

// Migrate spielt die eingebetteten Migrationen ein. goose arbeitet auf
// database/sql, deshalb die Bruecke ueber stdlib statt einer zweiten
// Verbindungskonfiguration; das Schliessen schliesst den Pool nicht.
func (s *Store) Migrate(ctx context.Context) error {
	db := stdlib.OpenDBFromPool(s.pool)
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(migrationFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}
