load('ext://restart_process', 'docker_build_with_restart')

# Harte Sicherung: Tilt laeuft nur gegen den lokalen Cluster.
if k8s_context() != 'kind-stateinspector':
    fail('Falscher Kontext: %s. Bitte "just up" nutzen.' % k8s_context())

local_resource(
    'compile',
    'CGO_ENABLED=0 GOOS=linux go build -o bin/stateinspector ./cmd/stateinspector',
    deps=['cmd', 'internal', 'api', 'go.mod', 'go.sum'],
    labels=['build'],
)

docker_build_with_restart(
    'stateinspector',
    '.',
    dockerfile='dev/Dockerfile.tilt',
    entrypoint=['/app/stateinspector'],
    only=['./bin/stateinspector'],
    live_update=[sync('./bin/stateinspector', '/app/stateinspector')],
)

k8s_yaml('dev/postgres.yaml')
k8s_yaml('dev/sample-workloads.yaml')
k8s_yaml(kustomize('config/dev'))

k8s_resource('postgres', port_forwards=['5432:5432'], labels=['infra'])
k8s_resource(
    'stateinspector',
    resource_deps=['compile', 'postgres'],
    port_forwards=['8080:8080', '8081:8081'],
    labels=['app'],
)
k8s_resource('web', labels=['demo'])
k8s_resource('api', labels=['demo'])
