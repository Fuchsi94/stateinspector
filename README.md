# stateinspector

Self-hosted Zustandshistorie fuer Kubernetes: Was lief wann wo, und was hat sich geaendert.
Laeuft komplett im eigenen Cluster, liest nur, und stellt die Daten ueber einen MCP-Server fuer beliebige LLMs bereit.

Beobachtet werden Deployments, StatefulSets, DaemonSets, Services, Ingresses und ConfigMaps.
Jede echte Aenderung wird als vollstaendiges normalisiertes Objekt plus RFC-6902-Patch gespeichert.
Rauschen — Status-Updates, ReplicaSet-Rollouts, `resourceVersion` — erzeugt keinen Eintrag.

## Voraussetzungen
Docker, Go (siehe go.mod), [just](https://github.com/casey/just), [kind](https://kind.sigs.k8s.io),
[ctlptl](https://github.com/tilt-dev/ctlptl), [Tilt](https://tilt.dev), kubectl, golangci-lint.
Optional: direnv (`direnv allow`).

## Quickstart
```sh
just up        # kind-Cluster + Registry + Tilt (Operator, Postgres, Demo-Workloads)
just churn     # Test-Changes erzeugen
just psql      # Changes ansehen
just check     # lint + unit + envtest
just e2e       # E2E gegen den laufenden Cluster
just down      # alles abraeumen
```

Die Kubeconfig liegt repo-lokal in `.kube/config` und enthaelt nur den kind-Cluster.

## MCP
Tilt forwarded den MCP-Server auf `http://localhost:8081/mcp`. Ohne Tilt: `just mcp`.
In Claude Code:
```sh
claude mcp add --transport http stateinspector http://localhost:8081/mcp
```

## Was man fragen kann

Die folgenden Antworten stammen aus einem lokalen `just up` plus `just churn`, gekuerzt.

**„Was hat sich seit gestern in Namespace demo geaendert?"** → `list_changes(since="24h", namespace="demo")`

```json
{
  "changes": [
    {
      "change_type": "updated",
      "changed_paths": ["/spec/replicas"],
      "images": ["nginx:1.27-alpine"],
      "kind": "Deployment", "namespace": "demo", "name": "web",
      "observed_at": "2026-09-29T14:39:14.55169Z",
      "offline": false
    },
    {
      "change_type": "updated",
      "changed_paths": ["/metadata/labels/team"],
      "images": ["ghcr.io/stefanprodan/podinfo:6.7.1"],
      "kind": "Deployment", "namespace": "demo", "name": "api",
      "observed_at": "2026-09-29T14:39:14.54821Z",
      "offline": false
    }
  ],
  "truncated": false
}
```

**„Welche Image-Version laeuft von web?"** → `get_versions(namespace="demo", name_contains="web")`

```json
{
  "workloads": [
    {
      "kind": "Deployment", "namespace": "demo", "name": "web",
      "images": ["nginx:1.27"],
      "last_changed": "2026-09-29T14:39:11.587055Z"
    }
  ],
  "truncated": false
}
```

**„Wie sah Deployment web frueher aus?"** → `get_resource(kind="Deployment", namespace="demo", name="web", at="2026-09-29T14:27:35.49485Z")`

```json
{
  "found": true,
  "observed_at": "2026-09-29T14:27:35.49485Z",
  "object": {
    "apiVersion": "apps/v1", "kind": "Deployment",
    "metadata": { "name": "web", "namespace": "demo", "labels": { "app": "web", "team": "frontend" } },
    "spec": { "replicas": 1, "template": { "spec": { "containers": [
      { "name": "nginx", "image": "nginx:1.27", "ports": [ { "containerPort": 80 } ] }
    ] } } }
  }
}
```

**„Was genau wurde beim letzten Rollout von web geaendert?"** → `diff_resource(kind="Deployment", namespace="demo", name="web", from="2026-09-29T14:27:35.49485Z")`

```json
{
  "found": true,
  "from_observed_at": "2026-09-29T14:27:35.49485Z",
  "to_observed_at": "2026-09-29T14:39:14.55169Z",
  "changed_paths": ["/spec/replicas", "/spec/template/spec/containers/0/image"],
  "patch": [
    { "op": "replace", "path": "/spec/replicas", "value": 3 },
    { "op": "replace", "path": "/spec/template/spec/containers/0/image", "value": "nginx:1.27-alpine" }
  ]
}
```

Das fuenfte Tool ist `list_resources` — die Uebersicht, was ueberhaupt beobachtet wird,
mit `include_deleted=true` auch das, was verschwunden ist.

Zeitangaben gehen als RFC 3339 oder als relative Dauer (`30m`, `24h`, `7d`) rein und kommen
immer als UTC mit Sekundenbruchteilen zurueck — ein zurueckgegebenes `observed_at` kann also
direkt wieder als `at` oder `from` verwendet werden.

## Grenzen

- **Keine Retention.** Die Historie waechst unbegrenzt; jede Version speichert das volle
  normalisierte Objekt als JSONB. Fuer den MVP bewusst so. Vor einem laengeren Einsatz die
  tatsaechliche Datenmenge messen und eine Aufraeumstrategie festlegen.
- **Keine Authentifizierung am MCP-Server.** Er bindet auf `127.0.0.1` und ist nur per
  Port-Forward erreichbar. Nicht ins Netz haengen.
- **Kein „wer hat es geaendert".** Die Spalte `actor` existiert und bleibt NULL, bis die
  Audit-Log-Anbindung kommt.
- **Keine Pods, keine Image-Digests, kein Multi-Cluster.** Die Historie beschreibt gewollten
  Zustand, nicht Laufzeit.
- **ConfigMap-Werte werden nie gespeichert** — weder in der Datenbank noch im Informer-Cache.
  Abgelegt werden nur die Schluesselnamen und ein Hash ueber den Inhalt.
