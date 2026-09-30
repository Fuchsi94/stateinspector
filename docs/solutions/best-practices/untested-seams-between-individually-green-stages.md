---
module: internal/collector
date: 2026-09-30
problem_type: best_practice
component: testing_framework
severity: high
symptoms:
  - "ConfigMap-Schluesselliste enthielt die Literale keys und sha256 statt der echten Schluesselnamen, in jeder Umgebung"
  - "Historieneintraege gingen bei jedem Shutdown und jeder Leader-Uebergabe still verloren"
  - "CI-e2e-Job scheiterte beim ersten echten GitHub-Lauf, obwohl lokal immer gruen"
applies_when:
  - "ein Wert durchlaeuft zwei oder mehr einzeln getestete Verarbeitungsstufen, bevor er sein Ziel erreicht"
  - "die Test-Suite ist strikt pro Komponente organisiert, sodass kein Test die echte Reihenfolge ausfuehrt"
  - "ein Shutdown- oder Uebergabepfad verwendet einen bereits abgebrochenen Context weiter, statt ihn zu entkoppeln"
  - "ein Fake oder Mock bildet die Randbedingung nicht nach, an der die echte Abhaengigkeit scheitert"
  - "ein CI-Job oder Umgebungspfad ist im Repo vorhanden, aber noch nie ausgefuehrt worden"
root_cause: test_isolation
resolution_type: test_fix
related_components:
  - collector
  - normalize
  - mcp-server
  - ci-e2e
tags:
  - seam-testing
  - integration-gap
  - context-cancellation
  - double-transformation
  - ci-first-run
  - code-review-catch
---

# Naht-Bugs: Warum zwei gruene Stufen zusammen trotzdem falsch sind

## Context

> Die Vorfallsakte zu den drei Faellen — Symptome, Zeitpunkte, Fixes — steht in
> `docs/progress/PROGRESS.md` (Abschnitt "Code-Review 2026-09-30" und
> "Learnings"). Dieser Eintrag wiederholt sie nur so weit, wie die Regeln
> darunter sonst abstrakt blieben; neu ist hier die uebertragbare Seite.


Waehrend der stateinspector-MVP (PR #1, `Fuchsi94/stateinspector`, offen zum
Zeitpunkt dieses Eintrags) entstanden ist, hatte die Test-Suite pro
Komponente ihre eigene, in sich konsistente Abdeckung: `internal/normalize`
testet Normalisierung isoliert per Golden-Fixtures, `internal/collector` hat
Unit-Tests fuer Writer und Handler, und ein separater envtest prueft gegen
eine echte API-Server-Instanz. Jede dieser Suiten war fuer sich gruen. Die
beiden schwersten Fehler des MVP sassen trotzdem beide dort, wo zwei Stufen
aufeinandertreffen, die je einzeln getestet waren — nicht *in* einer Stufe.
Ein dritter Fall derselben Form kam ueber CI dazu, bevor der Job je gelaufen
war.

### Instanz 1 — ConfigMap-Schluesselliste zerstoert, jede Umgebung, immer

Der Cache-Transform `StripConfigMapValues` (`internal/collector/transform.go:18`)
laeuft als `cache.ByObject`-`Transform` fuer ConfigMaps, verdrahtet in
`cacheByObject()` in `cmd/stateinspector/main.go` (Zeile 183:
`configMap: {Transform: collector.StripConfigMapValues}`). Er ersetzt
`data`/`binaryData` durch `{keys, sha256}`, **bevor** das Objekt im
Informer-Store landet. Die Normalisierungsregel fuer ConfigMaps
(`internal/normalize/rules.go:41-44`) rief `summarizeData("data")` und
`summarizeData("binaryData")` dann ein zweites Mal auf demselben, bereits
zusammengefassten Feld auf. Ergebnis: jede gespeicherte ConfigMap trug
`keys: ["keys","sha256"]` statt der echten Schluesselnamen — bestaetigt durch
eine Abfrage gegen die laufende Cluster-Datenbank vor dem Fix.

Warum die Tests das nicht sahen:
- Der Golden-Test (`internal/normalize/normalize_test.go`, Fixtures unter
  `internal/normalize/testdata/golden/configmap/`) fuettert `normalize.Object`
  direkt mit einem **rohen** Objekt — eine Anwendung, korrektes Ergebnis.
- Der envtest (`internal/collector/collector_envtest_test.go:279`,
  `TestConfigMapValuesNeverReachTheDatabase`) prueft per SQL nur, dass
  ConfigMap-**Werte** nie in der Datenbank landen (Zeile 309:
  `WHERE object::text LIKE '%geheim-%'`) — das erfuellt ein doppelt
  zusammengefasstes Objekt genauso.

Keiner der beiden Tests fuehrte Transform-dann-Normalisierung aus, was aber
genau der Produktionspfad ist.

Fix: `summarizeData` wurde idempotent. `alreadySummarized`
(`internal/normalize/rules.go:52-61`) erkennt die selbst erzeugte Form daran,
dass `keys` eine Liste ist. Der Diskriminator ist tragfaehig, weil die
`data`-Map einer echten ConfigMap aus `map[string]string` kommt — ihre Werte
sind also immer Strings; eine Liste an dieser Stelle kann nur von der eigenen
Zusammenfassung stammen (Kommentar dazu in Zeile 47-51). Damit eine
ConfigMap mit den zufaelligen Schluesseln "keys"/"sha256" nicht faelschlich
uebersprungen wird, prueft `alreadySummarized` zusaetzlich exakt zwei
Eintraege und einen 64-Zeichen-Hex-String bei `sha256`
(`internal/normalize/rules.go:52-61`).

Neuer Regressionstest: `TestTransformThenNormalizeKeepsRealKeyNames`
(`internal/collector/transform_test.go:120-146`) fuehrt die echte Sequenz
aus — Transform, dann `normalize.Object` — und prueft sowohl, dass die
echten Schluesselnamen ankommen, als auch, dass sie nicht ein zweites Mal
zusammengefasst wurden. Gegengeprueft: mit entferntem Guard schlaegt er fehl
mit `keys = "keys,sha256"`.

### Instanz 2 — P0, jeder Shutdown verlor bis zu 100 Eintraege

`Writer.Run` (`internal/collector/writer.go:81`) flushte beim
`ctx.Done()`-Fall urspruenglich den letzten Batch mit exakt jenem bereits
abgelaufenen Context. `WriteChanges` beginnt mit `pool.Begin(ctx)`
(`internal/store/queries.go:174`), und pgx/puddle liefert fuer einen
beendeten Context sofort `ctx.Err()` zurueck — der Abschluss-Flush scheiterte
also garantiert immer. Mit `--leader-elect` ist Handover Routinebetrieb, kein
Ausnahmefall.

Das korrekte Muster existierte bereits ein Paket weiter:
`internal/mcp/server.go:100` faehrt den HTTP-Listener mit
`context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)` herunter.

Warum die Tests das nicht sahen: beide vorhandenen Writer-Tests loesten das
Ende durch **Schliessen des Input-Kanals** aus (`in: !open` in
`writer.go:124-128`), nie durch Context-Cancel — der `ctx.Done()`-Zweig
(`writer.go:115-123`) lief also in keinem existierenden Test.

Fix: der Abschluss-Flush laeuft jetzt auf einem entkoppelten, befristeten
Context — `context.WithTimeout(context.WithoutCancel(ctx),
w.opts.ShutdownFlushTimeout)` (`internal/collector/writer.go:119-120`), mit
Kommentar, der exakt das oben beschriebene Scheitern von `pool.Begin`
erklaert. Neuer Test:
`TestWriterFlushesPendingBatchAfterContextCancel`
(`internal/collector/handler_test.go:309-337`) schreibt einen Change in den
Kanal, wartet, cancelt den Context und prueft, dass genau ein Eintrag
geschrieben wurde. Gegengeprueft: mit dem Fix zurueckgenommen meldet der Test
`0 Aenderungen nach dem Shutdown gespeichert, want 1`.

Wichtige Randnotiz zum Test selbst: er hat nur deshalb Biss, weil `fakeSink`
absichtlich `ctx.Err()` zurueckgibt, wenn der Context beendet ist
(`internal/collector/handler_test.go:31-35`) — das spiegelt pgx' Verhalten
bewusst nach. Ein Fake, der den Context ignoriert, haette den Test so oder so
gruen gehalten.

### Instanz 3 — ein CI-Job, der noch nie gelaufen war

Der `e2e`-Job in `.github/workflows/ci.yaml:14-79` hatte vor diesem MVP nie
auf GitHub ausgefuehrt. Sein erster echter Lauf schlug fehl:
`config/base/namespace.yaml` setzt
`pod-security.kubernetes.io/enforce: restricted`, was den root-laufenden
Postgres-Pod ablehnt; nur das Dev-Overlay (das das CI-Overlay per
`resources: [../dev]` erbt, `config/ci/kustomization.yaml:8`) lockert das per
JSON-Patch auf `baseline`
(`config/dev/kustomization.yaml`, Patch auf `Namespace stateinspector` /
`pod-security.kubernetes.io~1enforce` → `baseline`). Lokal war das
unsichtbar, weil Tilt immer `config/dev` direkt anwendet und nie den
Base-Namespace isoliert sieht. Der Fix (Commit `aca091d`, "fix(ci): create
the namespace from the ci overlay before Postgres") stellt sicher, dass der
CI-Job den Namespace ueber das `ci`-Overlay statt der Base anlegt; der
erklaerende Kommentar dazu steht jetzt direkt im Workflow
(`.github/workflows/ci.yaml:37-41`).

## Guidance

Die drei Faelle haben dieselbe Form, unabhaengig vom betroffenen
Subsystem: **die Suite testet Stufen, nicht Uebergaenge.** Vier konkrete
Regeln folgen daraus:

1. **Ein Wert, der zwei Transformationsstufen durchlaeuft, braucht
   mindestens einen Test, der die echte Sequenz ausfuehrt — nicht nur jede
   Stufe isoliert.** Golden-Fixtures, die eine Stufe direkt fuettern, sind
   notwendig, aber nicht hinreichend. Wenn Stufe A absichtlich eine Form
   erzeugt, die Stufe B kennen muss (hier: das zusammengefasste
   `{keys, sha256}`), gehoert das Zusammenspiel selbst unter Test, sonst
   sieht keiner der beiden Tests die Verkettung.

2. **Ein Zweig, der nur bei Abbruch oder Shutdown laeuft, braucht einen
   Test, der tatsaechlich abbricht.** Ein Kanal-Close und ein
   Context-Cancel sind unterschiedliche Beendigungspfade mit
   unterschiedlichem Code dahinter (`writer.go:113-135` hat fuer beide
   einen eigenen `case`). Ein Test, der nur den einen ausloest, gibt keine
   Aussage ueber den anderen ab — auch wenn beide "sauber beenden" testen
   *wollen*.

3. **Ein Fake muss die Einschraenkung modellieren, die das echte
   Abhaengigkeitsverhalten erzwingt, sonst besteht der Test folgenlos.**
   `fakeSink.WriteChanges` gibt `ctx.Err()` zurueck, wenn der Context tot
   ist, weil `pgxpool.Pool.Begin` das genauso tut. Ohne diese eine Zeile
   waere der Regressionstest aus Instanz 2 gruen gewesen, ob der Fix da war
   oder nicht — er haette nichts geprueft.

4. **Ein Automatisierungspfad, der noch nie gelaufen ist, ist ungeprueft,
   nicht gruen.** Ein CI-Job ohne Historie traegt kein Signal, egal wie
   lange er im Repo steht. Er verdient denselben Argwohn wie neuer,
   ungetesteter Code — am besten einen bewussten ersten Lauf, bevor man
   sich auf ihn verlaesst.

Zusaetzlich, als billige Gegenprobe fuer jeden neuen Regressionstest: **Fix
zuruecknehmen, Test soll rot werden.** Das ist nicht optional-nett — es hat
in genau dieser Session einen der frueheren Tests als wertlos entlarvt (der
urspruengliche Fake ignorierte den Context; erst das Gegenpruefen zeigte,
dass der Test so oder so bestand). Ohne diesen Schritt haette der Test
Vertrauen erzeugt, das er nicht verdient.

## Why This Matters

Der fertige Code sieht jetzt korrekt aus: `alreadySummarized` hat einen
Kommentar, der die Idempotenz-Notwendigkeit erklaert
(`internal/normalize/rules.go:47-51`), und der Shutdown-Flush hat einen
Kommentar, der `pool.Begin` gegen einen toten Context erklaert
(`internal/collector/writer.go:88-90, 116-118`). Ein Leser, der nur den
finalen Code liest, sieht zwei saubere Guards mit guten Kommentaren — aber
nichts im Baum sagt ihm, dass **Naehte zwischen Stufen** das strukturelle
Risikogebiet dieses Repos sind, oder dass die Suite dort blind war, bis
jemand das explizit gepruefte. Die Kommentare erklaeren das *Was*, nicht das
*Warum die Suite es nicht gefangen hat* — und genau diese zweite Information
ist es, die verhindert, dass der naechste Uebergang (Normalisierung →
Speicherung, Speicherung → MCP-Serialisierung, oder was als naechstes
dazukommt) denselben blinden Fleck reproduziert. Ohne diesen Eintrag muesste
ein zukuenftiger Beitragende dieselbe Analyse — drei fast identische
Post-mortems — noch einmal selbst durchfuehren, um dasselbe Muster zu
erkennen.

`internal/collector/transform.go` und `internal/normalize/rules.go` sind in
diesem Repo bereits als Risikozone markiert: `.claude/skills/add-watched-resource/SKILL.md`
(Zeilen 40-43) nennt unter "Fehler, die oft passieren" explizit, dass
sensible Felder nur im Handler statt im Cache-Transform zu stripped
kritisch ist und dass die Normalisierungsregel den Fall zusaetzlich
idempotent behandeln muss. Dieser Eintrag ergaenzt das um die
testmethodische Seite: *warum* eine Suite das trotz korrekter Kommentare im
Code lange nicht faengt.

## When to Apply

- Bevor eine neue `cache.ByObject`-`TransformFunc` oder eine neue
  `normalize`-Regel fuer eine bereits transformierte Ressourcenart
  hinzukommt: pruefen, ob ein Test die Kette Transform → Normalisierung
  tatsaechlich in dieser Reihenfolge ausfuehrt (siehe
  `TestTransformThenNormalizeKeepsRealKeyNames` als Vorlage).
- Beim Hinzufuegen oder Aendern von `select`-Zweigen, die auf
  `ctx.Done()` reagieren (Writer, Handler, jeder weitere Consumer mit
  Graceful-Shutdown-Pfad): sicherstellen, dass mindestens ein Test den
  Context tatsaechlich cancelt statt nur den Input-Kanal zu schliessen.
- Beim Schreiben eines Fakes/Mocks fuer eine externe Abhaengigkeit
  (Datenbank-Pool, HTTP-Client, etc.): die fehlerauslösenden
  Randbedingungen der echten Abhaengigkeit nachbilden (hier:
  Context-Sensitivitaet von `pgxpool.Pool.Begin`), nicht nur die
  Erfolgsantwort.
- Nach jedem neuen Regressionstest, der einen Bug reproduzieren soll: Fix
  kurz zuruecknehmen, Testlauf beobachten, sicherstellen, dass er tatsaechlich
  rot wird — bevor der Test als Beleg fuer den Fix gilt.
- Vor dem Verlassen auf einen CI-Job, der neu hinzugefuegt wurde oder lange
  nicht ausgefuehrt wurde: einen echten Lauf abwarten und die Ergebnisse
  pruefen, statt "ist im Workflow definiert" mit "ist verifiziert"
  gleichzusetzen.

## Examples

**Golden-Test, der die Naht nicht sieht** (Instanz 1) —
`internal/normalize/normalize_test.go:36-37` iteriert Fixtures unter
`testdata/golden/*/*` und ruft direkt `normalize.Object(loadYAML(...))` auf
einem rohen Objekt auf. Das ist der richtige Test fuer "normalisiert
`normalize.Object` ein rohes Objekt korrekt" — aber die falsche Frage fuer
"was passiert, wenn das Objekt vorher schon den Cache-Transform durchlaufen
hat".

**Der Test, der die Naht sieht** (Instanz 1, Fix) —
`internal/collector/transform_test.go:120-146`:
```go
func TestTransformThenNormalizeKeepsRealKeyNames(t *testing.T) {
    raw := configMap(map[string]any{"LOG_LEVEL": "info", "PORT": "8080"})

    fromCache := transformed(t, raw)                 // Stufe 1: Cache-Transform
    normalized, err := normalize.Object(fromCache)    // Stufe 2: Normalisierung
    ...
    if strings.Contains(got, "sha256") {
        t.Errorf("keys = %q; das Feld wurde ein zweites Mal zusammengefasst", got)
    }
}
```
Der entscheidende Unterschied ist die Verkettung in zwei Zeilen — genau das,
was in Produktion (`cacheByObject()` in `cmd/stateinspector/main.go:183`,
gefolgt vom Normalisierungspfad) tatsaechlich passiert.

**Test, der den falschen Beendigungspfad nimmt** (Instanz 2, vorher) — die
bestehenden Writer-Tests schlossen `in` (`close(in)`), was
`writer.go:124-128` ausloest, nie `writer.go:115-123`.

**Test, der den richtigen Beendigungspfad nimmt** (Instanz 2, Fix) —
`internal/collector/handler_test.go:309-337`:
```go
ctx, cancel := context.WithCancel(context.Background())
w := collector.NewWriter(sink, in, collector.WriterOptions{
    BatchSize: 100, FlushInterval: time.Hour, // weder Groesse noch Ticker loesen aus
})
go func() { done <- w.Run(ctx) }()
in <- store.Change{...}
time.Sleep(200 * time.Millisecond) // im Puffer, noch nicht geschrieben
cancel()
```
`BatchSize`/`FlushInterval` sind bewusst so gewaehlt, dass ausschliesslich
der `ctx.Done()`-Zweig den Flush ausloesen kann — sonst waere unklar, welcher
Pfad den Eintrag tatsaechlich gespeichert hat.

**Fake mit Biss vs. Fake ohne Biss** (Instanz 2) —
`internal/collector/handler_test.go:31-35`, der entscheidende Teil:
```go
func (f *fakeSink) WriteChanges(ctx context.Context, changes []store.Change) error {
    ...
    if err := ctx.Err(); err != nil {
        return err   // spiegelt pgxpool.Pool.Begin bei totem Context
    }
```
Ohne diese drei Zeilen haette `TestWriterFlushesPendingBatchAfterContextCancel`
unabhaengig vom Fix bestanden, weil der Fake nie mit einem Fehler auf den
Abschluss-Flush reagiert haette.

**Automatisierungspfad ohne Historie** (Instanz 3) — der Kommentar, der
jetzt in `.github/workflows/ci.yaml:37-41` steht, erklaert die Reihenfolge
explizit, nachdem der erste echte Lauf sie brach:
```yaml
# Reihenfolge ist hier wesentlich: config/base setzt auf dem Namespace
# pod-security enforce=restricted, und Postgres laeuft als root. ...
# Das ci-Overlay erbt vom dev-Overlay, das auf baseline lockert - es muss
# also zuerst kommen.
- name: Namespace und Operator anlegen
  run: just k apply -k config/ci
```
Vor diesem Lauf stand im Workflow nur der Job selbst — kein Hinweis darauf,
dass er noch nie ausgefuehrt worden war.
