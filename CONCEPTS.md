# CONCEPTS

Die Woerter, die in diesem Projekt eine bestimmte Bedeutung haben. Jeder
Eintrag erklaert seinen Begriff fuer sich, ohne Codezugriff.

Bisher erfasst: der Beobachtungs- und Speicherpfad (Collector, Normalisierung,
Store). Andere Bereiche sind noch nicht geseedet.

## Normalisierung

Das Entfernen all dessen aus einem beobachteten Objekt, was der Server selbst
verwaltet, sodass zwei Beobachtungen derselben Absicht als gleich gelten.
Abgrenzung zur Serialisierung: die bildet ab, die Normalisierung entscheidet,
was ueberhaupt als Unterschied zaehlt.

Die Regeln sind bewusst pro Ressourcenart erweiterbar, und eine Regel muss
idempotent sein: derselbe Wert kann eine Stufe mehrfach durchlaufen, wenn ihn
vorher schon eine andere Stufe umgeformt hat, und ein zweiter Durchlauf darf
das Ergebnis nicht veraendern.

## Rauschen

Jede Feldaenderung, die keine Absichtsaenderung ist — vom Server gesetzte
Zaehler, Zeitstempel, verwaltende Metadaten, Fortschrittsmeldungen. Rauschen
ist das, was die Normalisierung entfernt; was uebrig bleibt, ist eine echte
Aenderung.

Die Unterscheidung ist die zentrale Qualitaetsaussage des Projekts: erzeugt
Rauschen Eintraege, wird die Historie unlesbar, noch bevor sie unvollstaendig
waere.

## Change

Eine aufgezeichnete Version eines Objekts: der vollstaendige normalisierte
Zustand zu diesem Zeitpunkt, zusammen mit dem Unterschied zur Vorgaengerversion
und den Pfaden, die sich geaendert haben. Abgrenzung zum blossen Diff: ein
Change traegt den ganzen Zustand, damit "wie sah das damals aus" eine einzelne
Abfrage bleibt und nicht aus Patches rekonstruiert werden muss.

Ein Change ist entweder Anlage, Aenderung oder Loeschung. Ob er als Anlage
gilt, entscheidet nicht der Ereignistyp, sondern ob das Objekt vorher schon
bekannt war — sonst erzeugte jeder Neustart Anlagen fuer alles, was laengst
existiert. Ein Change kann ausserdem als *offline* markiert sein: dann wurde er
nicht beobachtet, als er passierte, sondern nachtraeglich erschlossen, weil er
in eine Ausfallzeit fiel. Die Markierung unterscheidet Beobachtetes von
Rekonstruiertem und darf nicht stillschweigend entfallen.

## Resource

Der aktuelle Stand genau eines beobachteten Objekts, gefuehrt unter seiner vom
Cluster vergebenen Identitaet. Abgrenzung zum Change: die Resource ist die
Gegenwart, die Changes sind die Vergangenheit; beide entstehen aus demselben
Schreibvorgang und duerfen nicht auseinanderfallen.

Eine geloeschte Resource wird markiert, nicht entfernt — ihre Historie bleibt
sonst ohne Anker. Taucht dieselbe Identitaet danach wieder auf, ist das eine
Aenderung an einem bekannten Objekt, keine Neuanlage.

## Hash-Cache

Der im Arbeitsspeicher gehaltene Merkzettel, welcher normalisierte Zustand je
Objekt zuletzt als gespeichert gilt. Er existiert, damit der Normalfall
"nichts hat sich geaendert" ohne Datenbankabfrage auskommt.

Seine Eintraege sind eine Behauptung ueber die Datenbank, keine Kopie davon,
und deshalb gelten drei Regeln. Beim Start wird er aus dem gespeicherten Stand
vorgeladen, sonst fragt der erste Durchlauf fuer jedes Objekt einzeln nach.
Eine geloeschte Identitaet gehoert nicht hinein, sonst prallt ihre Rueckkehr am
Merkzettel ab. Und scheitert ein Schreibvorgang, muss der betroffene Eintrag
vergessen werden — sonst gilt eine nie gespeicherte Version als gespeichert und
die naechste Beobachtung desselben Zustands erzeugt nichts mehr.
