-- +goose NO TRANSACTION
-- +goose Up

-- list_changes wird meist ohne Namespace-Filter aufgerufen ("was hat sich
-- seit gestern geaendert"). Der Index aus 0001 beginnt mit (cluster, namespace),
-- fuer einen reinen Zeitbereich ueber alle Namespaces ist er damit unbrauchbar.
-- changes waechst append-only ohne Retention, das verschlechtert sich taeglich.
--
-- CONCURRENTLY, weil diese Migration im Gegensatz zu 0001/0002 auf eine bereits
-- gefuellte Tabelle trifft: ein gewoehnliches CREATE INDEX nimmt eine
-- ACCESS-EXCLUSIVE-Sperre und blockiert solange jeden Write des Collectors.
-- CONCURRENTLY vertraegt keine Transaktion, daher NO TRANSACTION oben.

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS changes_cluster_observed_at_idx
  ON changes (cluster, observed_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS changes_cluster_observed_at_idx;
-- +goose StatementEnd
