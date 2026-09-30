-- +goose Up

-- resources haelt den aktuellen Stand je Objekt. uid ist der Schluessel:
-- Namen koennen wiederverwendet werden, die UID nicht.
CREATE TABLE resources (
  uid          uuid PRIMARY KEY,
  cluster      text NOT NULL,
  api_group    text NOT NULL,
  api_version  text NOT NULL,
  kind         text NOT NULL,
  namespace    text NOT NULL DEFAULT '',
  name         text NOT NULL,
  hash         text NOT NULL,
  object       jsonb NOT NULL,
  images       text[] NOT NULL DEFAULT '{}',
  first_seen   timestamptz NOT NULL,
  last_changed timestamptz NOT NULL,
  deleted_at   timestamptz
);
CREATE INDEX ON resources (cluster, kind, namespace, name);

-- changes haelt jede Version vollstaendig. Damit ist "Zustand zum Zeitpunkt X"
-- eine einzelne Query und es braucht keine Snapshot-Tabelle.
CREATE TABLE changes (
  id            bigserial PRIMARY KEY,
  uid           uuid NOT NULL,
  cluster       text NOT NULL,
  api_group     text NOT NULL,
  kind          text NOT NULL,
  namespace     text NOT NULL DEFAULT '',
  name          text NOT NULL,
  change_type   text NOT NULL CHECK (change_type IN ('created','updated','deleted')),
  offline       boolean NOT NULL DEFAULT false,
  observed_at   timestamptz NOT NULL,
  hash          text NOT NULL,
  object        jsonb NOT NULL,
  patch         jsonb,
  changed_paths text[] NOT NULL DEFAULT '{}',
  images        text[] NOT NULL DEFAULT '{}',
  owner         text,
  -- actor bleibt im MVP NULL; die Audit-Log-Anbindung kommt spaeter.
  actor         text
);
CREATE INDEX ON changes (cluster, namespace, observed_at DESC);
CREATE INDEX ON changes (uid, observed_at DESC);
CREATE INDEX ON changes USING gin (images);

-- +goose Down
DROP TABLE changes;
DROP TABLE resources;
