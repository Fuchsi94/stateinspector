# stateinspector — Progress

Phase: review — PR #1 offen, CI gruen (2026-09-30)
Next: PR #1 durchsehen und mergen. Danach die offenen Punkte unten.

Remote: https://github.com/Fuchsi94/stateinspector (public)
PR: https://github.com/Fuchsi94/stateinspector/pull/1 — `main` ist der
Baseline-Commit (Geruest + Plan), der PR traegt den gesamten Code.

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

## Code-Review 2026-09-30

Elf Reviewer-Personas, kein Cross-Model-Pass (bewusst untersagt, adversarial lief
lokal — Unabhaengigkeit damit nicht attestiert). Artefakte lagen unter
`/tmp/compound-engineering-501/ce-code-review/20260930-102612-0794df5c/`.
Ergebnis: 1 P0, 10 P1, rund 12 P2/P3. Alles bis auf die unten genannten Punkte behoben.

### Was die gruenen Gates nicht gefangen haben — und warum

Die Tests pruefen jede Stufe isoliert, nicht die Uebergaenge. Beide schwersten
Fehler lebten genau dort:

- **ConfigMap-Schluessel zerstoert.** Der Cache-Transform fasst `data` zusammen,
  die Normalisierungsregel fasste das Ergebnis erneut zusammen — gespeichert wurde
  `keys: ["keys","sha256"]` statt der echten Namen, in jeder Umgebung, immer. Der
  Golden-Test fuettert ein *rohes* Objekt (eine Anwendung, korrekt), der envtest
  prueft nur die Abwesenheit von Werten (auch doppelt zusammengefasst erfuellt). Den
  Produktionspfad lief kein Test.
- **P0: Shutdown-Flush auf totem Context.** `flush()` nutzte den bereits
  gecancelten Context, `pool.Begin` scheiterte dadurch immer, jede Leader-Uebergabe
  verlor bis zu 100 Eintraege. Beide Writer-Tests schlossen den Kanal, statt den
  Context abzubrechen — der Zweig war nie getestet.

Fuer beide gibt es jetzt Regressionstests, und beide wurden gegengeprueft: mit
zurueckgedrehtem Fix werden sie rot.

### Bewusst nicht behoben

- **`--exclude-namespaces` verkleinert den Informer-Cache nicht**, es filtert nur im
  Handler; `kube-system` liegt also vollstaendig im Speicher. Der Weg waere ein
  `DefaultFieldSelector` mit `metadata.namespace !=`. Vorher muss verifiziert werden,
  dass der API-Server mehrere UND-verknuepfte Ungleichheiten auf `metadata.namespace`
  im Watch traegt — nicht geraten, sondern offen gelassen.
- **Kein Retry im Writer.** Ein gescheiterter Stapel ist weiterhin verloren; neu ist
  nur, dass der Handler die Hashes vergisst und die naechste Beobachtung den Eintrag
  wieder erzeugt. Ein echter Retry mit Backoff ist eine eigene Entscheidung.
- **`offline` wird bei `created` nicht gesetzt.** Ein waehrend des Ausfalls neu
  angelegtes Objekt kommt als `created` mit `offline=false`. Unterscheidbar waere das
  nur ueber "hat der Store fuer diesen Cluster ueberhaupt schon Zeilen" — eine
  Produktentscheidung, keine Fehlerbehebung.
- **goose nimmt keine Advisory Lock.** Die Default-RollingUpdate-Strategie laesst
  kurzzeitig zwei Pods zu; gleichzeitige `Migrate()` sind heute nur durch Timing
  ungefaehrlich.
- **`since`/`until` versus `from`/`to`** heissen in zwei Tools verschieden. Eine
  Vereinheitlichung braeche den bestehenden Werkzeugvertrag fuer wenig Gewinn.
- **Retention** bleibt wie entschieden post-MVP.

## Offene Punkte (im MVP)
- ~~CI-E2E deployt nichts~~ — erledigt in U13, auf GitHub gruen verifiziert (Run 36700494789).
  Beim ersten echten Lauf fiel noch eine Reihenfolge auf: `config/base` setzt auf dem
  Namespace `pod-security enforce=restricted`, was den als root laufenden Postgres-Pod
  abweist. Nur das dev-Overlay, von dem `config/ci` erbt, lockert auf `baseline` — das
  Overlay muss also vor Postgres angewendet werden. Lokal war das nie sichtbar, weil
  Tilt immer ueber `config/dev` geht.
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
- Die zwei schwersten Fehler des Reviews lagen beide zwischen zwei gruen getesteten
  Stufen, nicht in einer. Komponententests allein finden so etwas nicht; es braucht
  mindestens einen Test, der den Produktionspfad durchlaeuft.
- Ein CI-Job, der nie gelaufen ist, ist nicht gruen, sondern ungeprueft. Der Umbau sah
  lokal richtig aus und fiel beim ersten echten Lauf ueber eine Policy, die lokal ein
  Overlay stillschweigend abfaengt.
- controller-runtime v0.25 -> client-go v0.37, Mindest-Go 1.26. Die envtest-Version muss der
  CR-Minor folgen, nicht frei gewaehlt werden. Quelle: Kompatibilitaetsmatrix im
  controller-runtime README.
- MCP Go SDK v1.8.0 hat `NewInMemoryTransports()` — die envtest-Tests koennen die Tools
  in-process aufrufen, ohne HTTP zu sprechen.
- `stdlib.OpenDBFromPool` bruecken pgxpool -> `*sql.DB` fuer goose, ohne zweite
  Verbindungskonfiguration; das Schliessen des `*sql.DB` schliesst den Pool nicht.
- `golangci-lint-action` v6 kann keine `version: "2"`-Config lesen; dafuer braucht es >= v7,
  aktuell ist v9 (golangci-lint >= v2.1.0). Quelle: github.com/golangci/golangci-lint-action
