# Repo-lokale Kubeconfig: enthaelt nur den kind-Cluster, nie Codify- oder Kundencluster.
export KUBECONFIG := justfile_directory() / ".kube" / "config"

cluster := "kind-stateinspector"
envtest_k8s := "1.34.x"

default:
    @just --list

# Cluster + Registry erstellen und Tilt starten
up: cluster
    tilt up

# Nur Cluster + Registry erstellen
cluster:
    mkdir -p .kube
    ctlptl apply -f dev/cluster.yaml

# Tilt stoppen und Cluster loeschen
down:
    -tilt down
    ctlptl delete -f dev/cluster.yaml

# kubectl gegen den lokalen kind-Cluster (einziger erlaubter kubectl-Weg)
k *args:
    kubectl --context {{cluster}} {{args}}

# psql-Shell in die lokale Postgres
psql *args:
    kubectl --context {{cluster}} -n stateinspector exec -it statefulset/postgres -- psql -U stateinspector {{args}}

# Simuliert Changes an den Demo-Workloads
churn:
    kubectl --context {{cluster}} -n demo set image deployment/web nginx=nginx:1.27-alpine
    kubectl --context {{cluster}} -n demo scale deployment/web --replicas=3
    kubectl --context {{cluster}} -n demo label deployment/api team=payments --overwrite
    kubectl --context {{cluster}} -n demo create configmap churn-$(date +%s) --from-literal=k=v

# DeepCopy-Code generieren
generate:
    go tool controller-gen object paths=./api/...

# CRDs und RBAC aus Kubebuilder-Markern generieren
manifests:
    go tool controller-gen crd rbac:roleName=stateinspector-reader paths=./... output:crd:dir=config/base/crd output:rbac:dir=config/base/rbac

lint:
    golangci-lint run ./...

# Unit-Tests
test:
    go test ./... -short -race

# envtest (echter API-Server + etcd, kein Cluster)
test-env:
    KUBEBUILDER_ASSETS="$(go tool setup-envtest use {{envtest_k8s}} -p path)" go test ./... -tags envtest -race

# E2E gegen laufenden kind-Cluster (vorher: just up)
e2e:
    go test ./test/e2e/... -tags e2e -v -count=1

# Alles, was vor einem Commit gruen sein muss
check: lint test test-env

# MCP-Server lokal erreichbar machen
mcp:
    kubectl --context {{cluster}} -n stateinspector port-forward deployment/stateinspector 8081:8081
