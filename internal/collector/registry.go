// Package collector beobachtet Kubernetes-Ressourcen read-only und leitet
// echte Aenderungen an den Store weiter.
package collector

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Die Rechte des Controllers. Ausschliesslich lesend - jede Erweiterung hier
// ist laut CLAUDE.md eine Entscheidung, die nachgefragt werden muss.
//
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets;daemonsets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=services;configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch

// Watched ist die Liste der beobachteten Arten. Eine neue Art kostet hier eine
// Zeile, einen RBAC-Marker oben und eine Normalisierungsregel - siehe den Skill
// add-watched-resource.
var Watched = []schema.GroupVersionKind{
	{Group: "apps", Version: "v1", Kind: "Deployment"},
	{Group: "apps", Version: "v1", Kind: "StatefulSet"},
	{Group: "apps", Version: "v1", Kind: "DaemonSet"},
	{Group: "", Version: "v1", Kind: "Service"},
	{Group: "", Version: "v1", Kind: "ConfigMap"},
	{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"},
}
