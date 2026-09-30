-- +goose Up

-- get_resource und diff_resource suchen ein einzelnes Objekt zu einem
-- Zeitpunkt: cluster + kind + namespace + name, sortiert nach observed_at.
-- Die Indizes aus 0001 decken kind und name nicht ab, Postgres musste also
-- ueber alle Arten und Namen des Namespaces filtern.
CREATE INDEX ON changes (cluster, kind, namespace, name, observed_at DESC);

-- +goose Down
DROP INDEX changes_cluster_kind_namespace_name_observed_at_idx;
