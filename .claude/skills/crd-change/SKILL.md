---
name: crd-change
description: Vorgehen beim Anlegen oder Aendern von eigenen CRD-Types in api/ (z. B. InspectorConfig, BusinessService), inklusive Code-Generierung, Validierung, Status-Conditions und Versionierung. Immer verwenden, wenn Dateien in api/ angefasst werden oder der User eine neue CRD, ein neues Feld oder eine neue API-Version will.
---

# CRD aendern

1. Types in `api/<version>/` aendern. Jedes Feld mit Go-Doc-Kommentar (wird zur OpenAPI-Beschreibung).
2. Validierung ueber Kubebuilder-Marker, nicht im Controller:
   `+kubebuilder:validation:Required`, `Enum`, `Pattern`, `Minimum`, `+kubebuilder:default`.
   Komplexe Regeln per CEL: `+kubebuilder:validation:XValidation:rule="..."`.
3. Status immer als Subresource (`+kubebuilder:subresource:status`) mit `Conditions []metav1.Condition`
   und `ObservedGeneration`. Condition-Typen: `Ready`, bei Bedarf `Degraded`. Reasons in CamelCase.
4. `just generate` und `just manifests`. Generierte Dateien nie von Hand editieren.
5. Kompatibilitaet:
   - Neues optionales Feld in bestehender Version: ok.
   - Feld umbenennen, entfernen oder Pflichtfeld hinzufuegen: NICHT in bestehender Version.
     Neue Version anlegen (`v1alpha2`), alte als served behalten, Storage-Version markieren.
6. Reconcile-Loop: idempotent, Status nur ueber `Status().Update/Patch`, Finalizer nur wenn externe
   Aufraeumarbeit (z. B. DB-Eintraege) noetig ist.
7. Tests: envtest mit gueltigen und ungueltigen Objekten (Validierung greift beim API-Server),
   Reconcile-Test fuer Conditions.
8. `just check` gruen.
