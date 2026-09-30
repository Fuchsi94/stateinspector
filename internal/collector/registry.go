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

// WatchedKind ist eine beobachtete Art samt ihrem API-Plural.
//
// Der Plural steht ausdruecklich dabei und wird nicht aus dem Kind abgeleitet:
// die Kubernetes-Pluralformen sind unregelmaessig (Ingress -> ingresses,
// NetworkPolicy -> networkpolicies), und der Test, der einen vergessenen
// RBAC-Marker fangen soll, darf nicht selbst am Raten scheitern.
type WatchedKind struct {
	GVK schema.GroupVersionKind
	// Resource ist der Plural, wie er im RBAC steht.
	Resource string
}

// Watched ist die Liste der beobachteten Arten. Eine neue Art kostet hier eine
// Zeile, einen RBAC-Marker oben und eine Normalisierungsregel - siehe den Skill
// add-watched-resource.
var Watched = []WatchedKind{
	{GVK: schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}, Resource: "deployments"},
	{GVK: schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"}, Resource: "statefulsets"},
	{GVK: schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "DaemonSet"}, Resource: "daemonsets"},
	{GVK: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Service"}, Resource: "services"},
	{GVK: ConfigMapGVK, Resource: "configmaps"},
	{GVK: schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"}, Resource: "ingresses"},
}

// ConfigMapGVK ist einzeln benannt, weil der Cache-Transform sie braucht.
// Eine zweite literale Kopie im Manager-Setup koennte still von der Registry
// abdriften - anders als beim RBAC faenge das kein Test ab.
var ConfigMapGVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
