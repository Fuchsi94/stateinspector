//go:build envtest

// Package dbtest startet die Postgres-Instanz, gegen die die envtest-Suiten
// laufen. Hinter dem envtest-Tag, damit testcontainers nicht in den normalen
// Build geraet.
package dbtest

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Postgres-Version und Zugangsdaten stehen an einer Stelle; vorher trug sie
// jede der drei Suiten als eigene Kopie.
const (
	image    = "postgres:17-alpine"
	database = "stateinspector"
	user     = "stateinspector"
	password = "test"
)

// StartPostgres startet den Container und liefert Connection-String und
// Abbruchfunktion. Gedacht fuer TestMain, wo es kein *testing.T gibt.
func StartPostgres(ctx context.Context) (string, func(), error) {
	container, err := tcpostgres.Run(ctx, image,
		tcpostgres.WithDatabase(database),
		tcpostgres.WithUsername(user),
		tcpostgres.WithPassword(password),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(2*time.Minute)),
	)
	if err != nil {
		return "", nil, fmt.Errorf("start postgres container: %w", err)
	}

	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = testcontainers.TerminateContainer(container)
		return "", nil, fmt.Errorf("connection string: %w", err)
	}
	return url, func() { _ = testcontainers.TerminateContainer(container) }, nil
}

// Truncate leert die Tabellen zwischen zwei Tests. Bewusst ueber eine eigene
// Verbindung: der Store kennt kein Loeschen und soll auch keins bekommen.
func Truncate(ctx context.Context, databaseURL string) error {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("open truncate connection: %w", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, "TRUNCATE changes, resources"); err != nil {
		return fmt.Errorf("truncate tables: %w", err)
	}
	return nil
}
