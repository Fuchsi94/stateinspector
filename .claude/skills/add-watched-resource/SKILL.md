---
name: add-watched-resource
description: Checkliste, um eine neue Kubernetes-Ressourcenart (z. B. CronJob, HPA, NetworkPolicy, eine CRD wie Argo Application) vom Collector beobachten zu lassen. Immer verwenden, wenn eine Ressource zur Watch-Liste hinzugefuegt, entfernt oder ihre Normalisierung geaendert wird, auch wenn der User nur "wir sollten auch X tracken" sagt.
---

# Neue Ressource beobachten

Arbeite die Schritte in dieser Reihenfolge ab. Ueberspringe keinen.

1. **Rauschen verstehen.** Lege im kind-Cluster ein Beispielobjekt an (`just k apply -f ...`), aendere es und
   beobachte mit `just k get <kind> -o yaml -w`, welche Felder sich ohne echte Aenderung bewegen
   (Status, Controller-Annotations, Timestamps).
2. **GVK registrieren** in `internal/collector/registry.go` (Liste der beobachteten GVKs).
   Bei CRDs: nur beobachten, wenn die CRD im Cluster existiert (Discovery pruefen, sonst ueberspringen und loggen).
3. **RBAC-Marker** in `internal/collector/registry.go` ergaenzen:
   `// +kubebuilder:rbac:groups=<group>,resources=<plural>,verbs=get;list;watch`
   Danach `just manifests` und pruefen, dass `config/base/rbac/role.yaml` nur get/list/watch enthaelt.
4. **Normalisierungsregeln** in `internal/normalize/rules.go` fuer diese Art ergaenzen
   (zusaetzlich zu den globalen Regeln). Sensible Felder (Tokens, Passwoerter, Daten) entfernen oder hashen.
5. **Extraktion** in `internal/extract`: Images, Owner-Referenzen, relevante Labels, falls vorhanden.
6. **Tests:**
   - Golden-Test in `internal/normalize/testdata/<kind>/` mit rohem Objekt (`input.yaml`) und erwartetem
     Ergebnis (`want.json`). Mindestens ein Fall, in dem nur Rauschen geaendert wird und KEIN Change entstehen darf.
   - envtest-Fall in `internal/collector`: Objekt anlegen, aendern, loeschen → genau drei Change-Eintraege.
7. `just check` gruen, dann `just up`, `just churn` und in `just psql` pruefen, dass die Changes plausibel sind.

## Fehler, die oft passieren
- RBAC-Marker vergessen → Informer bekommt 403, Cache synct nie, Readiness bleibt rot.
- Status nicht entfernt → jede Minute ein Change.
- Secrets indirekt geladen (z. B. ueber `envFrom` aufgeloest) → verboten, nur Referenzen speichern.
