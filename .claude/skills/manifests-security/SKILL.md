---
name: manifests-security
description: Security-Review fuer Kubernetes-Manifests, RBAC, Dockerfiles und spaeter das Helm-Chart dieses Repos. Immer verwenden, wenn Dateien in config/, dev/, Dockerfile oder charts/ geaendert werden, neue Berechtigungen noetig scheinen, oder vor einem Release/Pilot-Deployment.
---

# Manifests und Security pruefen

Pruefe jede Aenderung gegen diese Liste und nenne Abweichungen explizit.

## RBAC
- ClusterRole `stateinspector-reader`: ausschliesslich `get`, `list`, `watch`.
- Keine Wildcards (`*`) in apiGroups, resources oder verbs.
- Kein Zugriff auf `secrets`, `pods/exec`, `pods/portforward`, `serviceaccounts/token`, `nodes/proxy`.
- Schreibrechte nur fuer Leases und Events im eigenen Namespace (Role, nicht ClusterRole).
- Pruefbefehl: `just k auth can-i --list --as=system:serviceaccount:stateinspector:stateinspector`

## Pod und Container
- `runAsNonRoot: true`, feste UID 65532, `seccompProfile: RuntimeDefault`
- `allowPrivilegeEscalation: false`, `capabilities.drop: [ALL]`, `readOnlyRootFilesystem: true`
  (Ausnahme nur im Dev-Overlay fuer Tilt)
- Resource Requests und Memory Limit gesetzt
- Namespace mit `pod-security.kubernetes.io/enforce: restricted`
- MCP-Server bindet nur auf `127.0.0.1`, Zugriff nur per Port-Forward, bis Auth existiert

## Images
- Produktion: `gcr.io/distroless/static:nonroot`, keine Shell
- Base-Images mit festem Tag, spaeter per Digest
- Vor Pilot: Image signieren (cosign), SBOM erzeugen (syft), Scan (trivy) ohne kritische Findings

## Daten
- Keine Secret-Werte, keine ConfigMap-Werte in DB, Logs oder MCP-Antworten
- Dev-Credentials nur in `dev/`, nie in `config/base`
