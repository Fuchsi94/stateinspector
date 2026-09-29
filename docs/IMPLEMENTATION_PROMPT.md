# Implementierungsprompt: stateinspector MVP (Phase 1 + 2)

> Diesen Prompt in Claude Code im Repo-Root verwenden. Lies zuerst `CLAUDE.md` und die Skills in `.claude/skills/`.

## Kontext

Wir bauen `stateinspector`, einen self-hosted Kubernetes-Controller, der den Zustand von Workloads
historisiert. Er beobachtet Ressourcen read-only, entfernt Rauschen, speichert jede echte Aenderung
als vollstaendige normalisierte Version plus Diff in Postgres und stellt die Historie ueber einen
MCP-Server bereit, damit ein beliebiges LLM Fragen beantworten kann wie:

- Was hat sich seit gestern in Namespace X geaendert?
- Welche Image-Version laeuft von Service Y?
- Wie sah Deployment Z letzten Montag aus?
- Was genau wurde beim letzten Rollout von Z geaendert?

Das Repo-Geruest existiert bereits: `justfile`, `Tiltfile`, `dev/`, `config/`, `Dockerfile`, CI,
`CLAUDE.md`, Skills. Es fehlt der gesamte Go-Code. Passe die bestehenden Dateien nur an, wenn es
technisch noetig ist, und begruende es.

## Ziele dieses Auftrags

1. Lauffaehiger Collector fuer: Deployments, StatefulSets, DaemonSets, Services, Ingresses, ConfigMaps (nur Metadaten).
2. Postgres-Speicherung mit Versionierung, Diffs und Tombstones.
3. MCP-Server mit fuenf read-only Tools.
4. Tests auf drei Ebenen (Unit, envtest, E2E), `just check` und `just e2e` gruen.
5. `just up` startet alles lokal, `just churn` erzeugt sichtbare Changes.

## Nicht-Ziele

- Keine CRDs (kommen in Phase 3), `api/` bleibt vorerst leer.
- Keine UI, kein Hub, kein Multi-Cluster, keine Cloud-APIs.
- Keine Pods beobachten. Image-Digests kommen spaeter.
- Kein "wer hat es geaendert" (Audit-Log-Anbindung kommt spaeter). Das Feld `actor` wird angelegt, bleibt aber NULL.
- Keine Authentifizierung am MCP-Server. Er bindet nur auf 127.0.0.1 und ist nur per Port-Forward erreichbar.

## Technische Vorgaben

- Go-Modul: `github.com/Fuchsi94/stateinspector`. Aktuelle stabile Go-Version.
- controller-runtime (aktuelle Version) als Basis: Manager, Cache, Leader Election, Health-Probes, Metrics.
- Tools als `tool`-Direktive in go.mod: `sigs.k8s.io/controller-tools/cmd/controller-gen`,
  `sigs.k8s.io/controller-runtime/tools/setup-envtest`.
- Postgres: `github.com/jackc/pgx/v5` (pgxpool). Migrationen mit `github.com/pressly/goose/v3`, SQL-Dateien per `embed`, beim Start ausfuehren.
- JSON-Patch: `github.com/wI2L/jsondiff` (RFC 6902).
- MCP: offizielles Go-SDK `github.com/modelcontextprotocol/go-sdk`, Streamable-HTTP-Transport unter `/mcp`.
- Tests mit Postgres: `github.com/testcontainers/testcontainers-go/modules/postgres`.
- Logging: `logr` via controller-runtime (zap).

## Flags

| Flag | Default | Bedeutung |
|---|---|---|
| `--cluster-name` | `local` | Name des Clusters, wird an jede Zeile geschrieben |
| `--leader-elect` | `false` | Leader Election aktivieren |
| `--metrics-bind-address` | `:8080` | controller-runtime Metrics |
| `--health-probe-bind-address` | `:8082` | `/healthz`, `/readyz` |
| `--mcp-bind-address` | `127.0.0.1:8081` | MCP-Server |
| `--namespaces` | leer = alle | Komma-Liste zu beobachtender Namespaces |
| `--exclude-namespaces` | `kube-system,kube-public,kube-node-lease` | ausgeschlossene Namespaces |

`DATABASE_URL` kommt aus der Umgebung und ist Pflicht.

## Paketstruktur

```
cmd/stateinspector/main.go     Flags, Manager, Store, Collector, MCP starten
internal/collector/            registry.go (GVK-Liste + RBAC-Marker), handler.go (generischer Event-Handler)
internal/normalize/            normalize.go, rules.go, hash.go, testdata/
internal/diff/                 diff.go (Patch + geaenderte Pfade)
internal/extract/              images.go, owners.go
internal/store/                store.go, queries.go, migrations/*.sql
internal/mcp/                  server.go, tools.go
test/e2e/                      e2e_test.go (Build-Tag e2e)
```

## Collector

- Beobachte alle GVKs aus `registry.go` ueber den controller-runtime Cache mit `unstructured.Unstructured`,
  damit ein generischer Handler fuer alle Arten reicht. RBAC-Marker fuer jede GVK in `registry.go`.
- Kein Reconcile-Loop im klassischen Sinn: Nutze Informer-Event-Handler (Add/Update/Delete) auf dem Cache
  des Managers. Der Handler laeuft nur beim Leader (Runnable mit `NeedLeaderElection() == true`).
- Ablauf pro Event:
  1. Objekt normalisieren, Hash berechnen (SHA-256 ueber kanonisches JSON mit sortierten Keys).
  2. Letzten bekannten Hash fuer die UID aus `resources` laden (In-Memory-Cache vor der DB ist erlaubt).
  3. Hash gleich → nichts tun. Hash anders → Diff gegen die letzte gespeicherte Version, Change schreiben, `resources` aktualisieren.
  4. Delete → Change vom Typ `deleted` mit letztem Objekt, `resources.deleted_at` setzen.
     Auch `DeletedFinalStateUnknown` (Tombstone) korrekt behandeln.
- Start und Resync: Beim initialen List entstehen Add-Events fuer alles. Unbekannte UID → `created`.
  Bekannte UID mit anderem Hash → `updated` mit `offline = true` (Aenderung passierte, waehrend der Collector lief nicht).
  Nach abgeschlossenem Cache-Sync: alle UIDs in `resources` ohne `deleted_at`, die nicht mehr im Cache sind, als `deleted` mit `offline = true` markieren.
- Schreiben ueber einen gepufferten Kanal mit einem Writer-Goroutine und Batch-Inserts (max. 100 Eintraege oder 1 s), damit der API-Event-Handler nie auf die DB blockiert. Bei vollem Puffer: blockieren statt verwerfen, Metrik exportieren.
- Readiness erst gruen, wenn Cache gesynct UND Migrationen durch UND DB erreichbar.

## Normalisierung

Globale Regeln fuer alle Arten:
- Entfernen: `status`, `metadata.managedFields`, `metadata.resourceVersion`, `metadata.generation`,
  `metadata.creationTimestamp`, `metadata.selfLink`, `metadata.uid` (UID wird separat gespeichert).
- Annotations entfernen: `kubectl.kubernetes.io/last-applied-configuration`,
  `deployment.kubernetes.io/revision`, alles mit Praefix `kubectl.kubernetes.io/restartedAt` NICHT entfernen (das ist ein echter Rollout).
- Leere Maps und Listen entfernen, damit `{}` und fehlend gleich hashen.

Artspezifisch:
- Deployment/StatefulSet/DaemonSet: Defaults, die der API-Server setzt, bleiben drin (sie sind stabil).
- Service: `spec.clusterIP`, `spec.clusterIPs` bleiben drin, `spec.ipFamilies` und `spec.ipFamilyPolicy` entfernen.
- ConfigMap: `data` und `binaryData` durch `{"keys": [...sortiert], "sha256": "<hash ueber Inhalt>"}` ersetzen. Werte nie speichern.

Regeln in `rules.go` als Tabelle `map[schema.GroupKind][]Rule`, damit neue Arten einfach ergaenzt werden.

## Extraktion

- `images`: alle `containers[].image` und `initContainers[].image` aus dem Pod-Template als `text[]`.
- `owner`: erste `ownerReference` mit `controller: true` als `kind/name`, sonst NULL.

## Datenmodell (erste Migration)

```sql
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

CREATE TABLE changes (
  id           bigserial PRIMARY KEY,
  uid          uuid NOT NULL,
  cluster      text NOT NULL,
  api_group    text NOT NULL,
  kind         text NOT NULL,
  namespace    text NOT NULL DEFAULT '',
  name         text NOT NULL,
  change_type  text NOT NULL CHECK (change_type IN ('created','updated','deleted')),
  offline      boolean NOT NULL DEFAULT false,
  observed_at  timestamptz NOT NULL,
  hash         text NOT NULL,
  object       jsonb NOT NULL,
  patch        jsonb,
  changed_paths text[] NOT NULL DEFAULT '{}',
  images       text[] NOT NULL DEFAULT '{}',
  owner        text,
  actor        text
);
CREATE INDEX ON changes (cluster, namespace, observed_at DESC);
CREATE INDEX ON changes (uid, observed_at DESC);
CREATE INDEX ON changes USING gin (images);
```

Bewusste Entscheidung: Jede Version speichert das vollstaendige normalisierte Objekt. Damit ist
"Zustand zum Zeitpunkt X" eine einfache Query (letzte Version pro UID mit `observed_at <= X`, nicht geloescht)
und wir brauchen im MVP keine Snapshot-Tabelle. JSONB wird von Postgres komprimiert (TOAST). Datenmenge im Test beobachten.

## MCP-Tools

Alle Tools sind read-only, Antworten als JSON-Text, Zeitangaben als RFC 3339 UTC.
`since`/`at`/`from`/`to` akzeptieren RFC 3339 oder relative Dauern wie `24h`, `7d`.

1. `list_changes(since, until?, namespace?, kind?, name?, limit=50)`
   → Liste mit `observed_at, change_type, offline, kind, namespace, name, changed_paths, images`. Kein volles Objekt.
2. `get_resource(kind, namespace, name, at?)`
   → normalisiertes Objekt zum Zeitpunkt `at` (Default: jetzt), plus `observed_at` der Version.
3. `diff_resource(kind, namespace, name, from, to?)`
   → RFC-6902-Patch zwischen den Versionen, die zu `from` und `to` gueltig waren, plus `changed_paths`.
4. `get_versions(namespace?, name_contains?)`
   → aktuelle Workloads mit `kind, namespace, name, images`, sortiert.
5. `list_resources(kind?, namespace?, include_deleted=false)`
   → Uebersicht mit `kind, namespace, name, last_changed, deleted_at`.

Tool-Beschreibungen so schreiben, dass ein LLM ohne weiteren Kontext weiss, wann es welches Tool nutzt.
Harte Limits: max. 500 Eintraege pro Antwort, Hinweis im Ergebnis, wenn abgeschnitten.

## Tests

- Unit (`just test`):
  - Golden-Tests fuer Normalisierung pro Art in `internal/normalize/testdata/<kind>/`.
    Pflichtfall pro Art: Nur Rauschfelder geaendert → identischer Hash.
  - ConfigMap: Werte tauchen im normalisierten Objekt nicht auf.
  - Diff: geaenderte Pfade korrekt, leere Patches bei gleichem Objekt.
  - Parsing relativer Zeitangaben.
- envtest (`just test-env`, Build-Tag `envtest`):
  - Postgres via testcontainers. Collector gegen envtest-API-Server starten.
  - Deployment anlegen → `created`. Image aendern → `updated` mit Pfad `/spec/template/spec/containers/0/image`.
    Nur Status patchen → KEIN Change. Loeschen → `deleted`.
  - Neustart-Szenario: Collector stoppen, Objekt aendern, Collector starten → ein `updated` mit `offline = true`.
  - MCP-Tools gegen die befuellte DB aufrufen (In-Process-Client des SDK).
- E2E (`just e2e`, Build-Tag `e2e`):
  - Nutzt den laufenden kind-Cluster (lokal via `just up`; in CI deployed der Test selbst Postgres und
    `config/dev` mit dem Image aus `E2E_IMAGE`).
  - `kubectl set image` auf `demo/web`, dann per MCP `list_changes(since=5m)` pruefen.

## Arbeitsweise

Arbeite in dieser Reihenfolge, jeder Schritt ein eigener Commit mit gruenem `just check`:

1. `go.mod`, Tool-Direktiven, leeres `main.go` mit Flags, Manager, Health-Probes. `just up` muss einen laufenden Pod zeigen.
2. `internal/normalize` + `internal/diff` + `internal/extract` mit Unit- und Golden-Tests.
3. `internal/store` mit Migrationen und Queries, Tests mit testcontainers.
4. `internal/collector` mit envtest-Tests inkl. Neustart-Szenario.
5. `internal/mcp` mit allen fuenf Tools und Tests.
6. E2E-Test, CI anpassen falls noetig.
7. README ergaenzen: Beispiel-Fragen und echte Beispielantworten aus dem lokalen Cluster.

Nach Schritt 4 und 6 den Skill `manifests-security` anwenden und Abweichungen beheben.

## Akzeptanzkriterien

- `just up` → Operator Ready innerhalb 60 s, `just churn` erzeugt genau die erwarteten Changes (keine Duplikate, kein Rauschen).
- Status-Updates, ReplicaSet-Rollouts und Leader-Election-Leases erzeugen keine Changes.
- `just k auth can-i --list --as=system:serviceaccount:stateinspector:stateinspector` zeigt nur get/list/watch
  (plus Leases/Events im eigenen Namespace). Keine Secrets.
- In DB, Logs und MCP-Antworten tauchen keine ConfigMap-Werte auf.
- Claude Code beantwortet ueber den MCP-Server die vier Fragen aus dem Kontext korrekt.
- `just check` und `just e2e` gruen, CI gruen.

## Wenn du unsicher bist

Frag nach, statt zu raten, bei: Aenderungen an RBAC, neuen Abhaengigkeiten ausserhalb der Liste,
Aenderungen am Datenmodell nach Schritt 3, und allem, was ins Cluster schreibt.
