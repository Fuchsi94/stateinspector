# stateinspector — Progress

Phase: work — U1 bis U14 implementiert und verifiziert (2026-09-29)
Next: Shipping — `ce-simplify-code`, Code-Review, dann PR. Danach erst ist der
MVP abgeschlossen; die Post-MVP-Punkte unten bleiben offen.

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

## Stand 2026-09-29 — MVP implementiert

Alle 14 Units umgesetzt, `just check` und `just e2e` gruen, Akzeptanzkriterien gegen
einen echten kind-Cluster geprueft:

- `just churn` erzeugt genau vier Eintraege (Image, Replicas, Label, neue ConfigMap),
  null Duplikate, null ReplicaSet-/Pod-/Lease-Rauschen.
- RBAC zeigt ausschliesslich `get`/`list`/`watch` auf die sechs Arten. Leader-Election
  ist eine namespaced Role, keine ClusterRole.
- Kein ConfigMap-Wert in `changes` oder `resources` (per SQL geprueft, 0 Treffer).
- Die vier Leitfragen sind ueber MCP beantwortbar; `test/e2e/questions_test.go` haelt
  das als Test fest.

### Zwei Funde, die die Tests erzwungen haben

- **Kein Unit haengte den Collector an den Manager.** Der Plan definierte Handler,
  Writer und Sweeper, aber keine Unit verdrahtete sie. Ohne `runner.go` waere der
  Collector nie angelaufen. Nachgezogen als eigener Commit.
- **MCP-Zeitstempel waren nicht rundreisefaehig.** `observed_at` ging auf Sekunden
  gerundet raus, gespeichert wird mikrosekundengenau. Ein zurueckgegebener Zeitstempel
  als `at` eingesetzt schnitt per `observed_at <= at` genau die gesuchte Version weg.
  Behoben durch RFC3339Nano in der MCP-Schicht.

### Datenmenge (Basis fuer die Retention-Entscheidung)

Nach `just up` + `just churn` im kind-Cluster: **60 Eintraege in `changes`, 224 kB
inklusive Indizes, rund 3.8 kB pro Eintrag** (JSONB wird von Postgres via TOAST
komprimiert). `resources`: 18 Zeilen, 96 kB. Hochgerechnet kostet ein Cluster mit
500 beobachteten Objekten und 20 Aenderungen pro Tag rund 28 MB im Jahr — unkritisch,
aber unbegrenzt wachsend. Die Zahl ist der Ausgangspunkt fuer die Retention-Frage.

## Offene Punkte (im MVP)
- ~~CI-E2E deployt nichts~~ — erledigt in U13: der Job deployt Postgres, Operator und
  Demo-Workloads, `config/ci` pinnt das gebaute Image. **Noch nicht auf GitHub gelaufen**,
  da kein Remote konfiguriert ist; lokal ist der aequivalente Pfad gruen.
- ~~Versions-Achse~~ — erledigt in U1: Go 1.27, controller-runtime v0.25.1,
  envtest 1.37.0, k8s.io/api v0.37.0.
- ~~Index-Patch auf `args/1`~~ — erledigt in U14: Strategic-Merge ersetzt die Liste als Ganzes.
- **Neu gefunden, nicht behoben:** `just psql` reicht `{{args}}` unquotiert weiter und nutzt
  `-it`. `just psql -c "SELECT ..."` zerfaellt damit in Einzelargumente und scheitert ohne
  TTY. Fuer interaktiven Gebrauch funktioniert die Recipe; fuer Skripte braeuchte es eine
  eigene `just sql <query>`. Bewusst nicht im Rahmen von U14 geaendert.

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
