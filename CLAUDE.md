# stateinspector

Self-hosted Kubernetes-Zustandshistorie. Ein Controller liest Workloads read-only ueber die K8s-API,
normalisiert sie, speichert jede Aenderung in Postgres und stellt die Historie ueber einen MCP-Server bereit.

## Architektur
- `cmd/stateinspector` — main, Flags, Manager-Setup
- `internal/collector` — generischer Watch-Handler pro GVK (controller-runtime Cache/Informers)
- `internal/normalize` — Felder entfernen, die kein echter Change sind; Hash berechnen
- `internal/diff` — RFC 6902 Patch zwischen zwei normalisierten Objekten, geaenderte Pfade
- `internal/store` — Postgres (pgx v5), Migrationen embedded (goose)
- `internal/mcp` — MCP-Server (streamable HTTP, nur 127.0.0.1)
- `internal/extract` — Images, Owner, Labels aus Objekten ziehen
- `api/` — CRD-Types (ab Phase 3)
- `test/e2e` — E2E gegen kind
- `docs/solutions/` — dokumentierte Loesungen vergangener Probleme (Bugs, Praktiken,
  Workflow-Muster), nach Kategorie abgelegt mit YAML-Frontmatter (`module`, `tags`,
  `problem_type`). Relevant beim Implementieren oder Debuggen in bereits
  dokumentierten Bereichen.
- `CONCEPTS.md` — gemeinsames Domain-Vokabular (Normalisierung, Rauschen, Change,
  Resource, Hash-Cache). Relevant beim Einarbeiten und wenn ueber diese Begriffe
  gesprochen wird.

## Befehle
- `just up` / `just down` — lokaler kind-Cluster + Tilt
- `just check` — lint + unit + envtest. MUSS gruen sein vor jedem Commit.
- `just e2e` — E2E, braucht laufendes `just up`
- `just churn` — erzeugt Test-Changes in Namespace `demo`
- `just k <args>` — kubectl gegen den lokalen Cluster
- `just psql` — SQL-Shell

## Harte Regeln
- kubectl NIE direkt aufrufen, nur ueber `just k`. Kein anderer Kontext als `kind-stateinspector`.
- RBAC des Controllers: nur `get`, `list`, `watch`. Einzige Ausnahme: Leases/Events im eigenen Namespace.
- Secrets werden nie gelesen, nicht im RBAC, nicht im Cache, nicht in Tests.
- ConfigMaps: nur Metadaten, Keys und Hash der Daten speichern, nie die Werte.
- Kein Code, der ins Cluster schreibt (kein Create/Update/Patch/Delete auf fremde Ressourcen).
- Neue Abhaengigkeiten nur mit Begruendung im PR/Commit.
- Tools ueber `go tool` (go.mod tool-Direktive), nicht global installiert.

## Konventionen
- Go: Fehler mit `%w` wrappen, `context.Context` als erster Parameter, keine globalen Variablen ausser Flags.
- Logging: `logr` aus controller-runtime, strukturierte Keys (`gvk`, `namespace`, `name`, `uid`).
- Tests: table-driven, Golden Files in `testdata/`. Neue Normalisierungsregel = neuer Golden-Test.
- Zeit immer UTC, `timestamptz` in Postgres.
- Commits klein und in sich lauffaehig.

## Skills
- `add-watched-resource` — neue Ressourcenart beobachten
- `crd-change` — CRD-Types aendern
- `manifests-security` — Manifests, RBAC und Container-Security pruefen
