# stateinspector — Progress

Phase: work (MVP Phase 1+2 laut `docs/IMPLEMENTATION_PROMPT.md`)
Next: U1 des Plans — Go-Modul auf Go 1.27, Tool-Direktiven, Versions-Achse in `Dockerfile`
und `justfile` an controller-runtime v0.25 angleichen.

## Artefakte
- Plan: `docs/plans/2026-09-29-stateinspector-mvp.md` (14 Units, Verification Contract, Definition of Done)
- Origin/Requirements: `docs/IMPLEMENTATION_PROMPT.md` (7 Schritte, je ein Commit mit gruenem `just check`)
- Repo-Regeln: `CLAUDE.md`
- Skills: `.claude/skills/{add-watched-resource,crd-change,manifests-security}`

## Entscheidungen

### 2026-09-29 — Scoping-Review des Geruests
Kein Brainstorm noetig: der Implementierungsprompt ist bereits ein vollstaendiger Plan
(Ziele, Nicht-Ziele, Schema, Paketstruktur, Tool-Signaturen, Akzeptanzkriterien).

| # | Thema | Entscheidung |
|---|---|---|
| 1 | Go-Modulpfad | `github.com/Fuchsi94/stateinspector` — festgelegt, kein Platzhalter mehr |
| 2 | ConfigMap-Werte im Informer-Cache | **in den MVP gezogen** (U10) — `cache.Options` bietet `Transform` als Einbauteil |
| 3 | Retention / Pruning der `changes`-Tabelle | **post-MVP** |
| 4 | CI-E2E deployt den Operator nicht | **im MVP fixen**, siehe offene Punkte |
| 5 | golangci-lint-action Version | erledigt: v6 -> v9 (v2-Config braucht Action >= v7) |
| 6 | `resources` PK `(cluster, uid)` | verworfen — Multi-Cluster ist nicht geplant, `uid` bleibt PK |

Repo-Verzeichnis am 29.09. von `k8s-inspector` auf `stateinspector` umbenannt, passend zum
Modulpfad und zum Projektnamen im Geruest.

## Offene Punkte (im MVP)
- **Schritt 6, CI-E2E:** Der `e2e`-Job macht aktuell nur `kubectl apply -k config/base
  --dry-run=client`, deployt also weder Postgres noch den Operator. Zusaetzlich referenziert
  `config/dev` hart das Image `stateinspector`, nicht `stateinspector:ci` aus `E2E_IMAGE` —
  kustomize hat dort keinen `images:`-Transformer. Beides beim E2E-Schritt aufloesen.
- **Versions-Achse (verifiziert, jetzt U1):** controller-runtime v0.25 verlangt Go >= 1.26 und
  zielt auf client-go v0.37. `Dockerfile` pinnt `GO_VERSION=1.25` und baut damit nicht;
  `justfile` pinnt `envtest_k8s := "1.34.x"`, was zu CR v0.22 gehoert. Beides zieht auf die
  neueste Achse (Go 1.27, envtest 1.37.x).
- Kleinigkeit: `config/dev/kustomization.yaml` patcht `args/1` per Index. Reordert jemand die
  Flags, greift der Patch still das falsche Argument.

## Post-MVP-Backlog
- **Retention.** Kein Pruning, keine TTL. Jede Version speichert das volle normalisierte Objekt
  als JSONB; `just churn` legt bei jedem Aufruf eine neue ConfigMap an. Die `changes`-Tabelle
  waechst unbegrenzt.
- Aus dem Prompt bereits als Nicht-Ziel markiert: CRDs (Phase 3), `actor` / Audit-Log-Anbindung,
  Image-Digests, UI, Multi-Cluster, Auth am MCP-Server.

## Learnings
- controller-runtime v0.25 -> client-go v0.37, Mindest-Go 1.26. Die envtest-Version muss der
  CR-Minor folgen, nicht frei gewaehlt werden. Quelle: Kompatibilitaetsmatrix im
  controller-runtime README.
- MCP Go SDK v1.8.0 hat `NewInMemoryTransports()` — die envtest-Tests koennen die Tools
  in-process aufrufen, ohne HTTP zu sprechen.
- `stdlib.OpenDBFromPool` bruecken pgxpool -> `*sql.DB` fuer goose, ohne zweite
  Verbindungskonfiguration; das Schliessen des `*sql.DB` schliesst den Pool nicht.
- `golangci-lint-action` v6 kann keine `version: "2"`-Config lesen; dafuer braucht es >= v7,
  aktuell ist v9 (golangci-lint >= v2.1.0). Quelle: github.com/golangci/golangci-lint-action
