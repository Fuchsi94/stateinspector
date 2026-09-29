# stateinspector

Self-hosted Zustandshistorie fuer Kubernetes: Was lief wann wo, was hat sich geaendert, wer hat es geaendert.
Laeuft komplett im eigenen Cluster, liest nur, und stellt die Daten ueber einen MCP-Server fuer beliebige LLMs bereit.

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
Tilt forwarded den MCP-Server auf `http://localhost:8081/mcp`. In Claude Code:
```sh
claude mcp add --transport http stateinspector http://localhost:8081/mcp
```
