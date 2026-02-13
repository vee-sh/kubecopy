package discovery

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/a13x22/kubecopy/pkg/copier"
)

// refKey uniquely identifies a resource for cycle detection.
type refKey struct {
	Resource  string
	Name      string
	Namespace string
}

// Discover finds all related resources for the given primary resource.
// Returns additional ResourceRefs that should be copied alongside the primary.
// Uses BFS to traverse the dependency graph with cycle detection.
func Discover(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource, name, namespace string) ([]copier.ResourceRef, error) {
	visited := map[refKey]bool{}
	var result []copier.ResourceRef

	// Mark the primary resource as visited
	primaryKey := refKey{Resource: gvr.Resource, Name: name, Namespace: namespace}
	visited[primaryKey] = true

	// Fetch the primary object
	primaryObj, err := client.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("fetching primary resource %s/%s: %w", gvr.Resource, name, err)
	}

	// BFS queue
	type queueItem struct {
		obj *unstructured.Unstructured
		ref copier.ResourceRef
	}
	queue := []queueItem{{obj: primaryObj, ref: copier.ResourceRef{GVR: gvr, Name: name, Namespace: namespace}}}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		// Discover forward references (ConfigMaps, Secrets, PVCs, ServiceAccounts)
		forwardRefs := extractForwardRefs(current.obj, namespace)
		for _, ref := range forwardRefs {
			key := refKey{Resource: ref.GVR.Resource, Name: ref.Name, Namespace: ref.Namespace}
			if visited[key] {
				continue
			}
			visited[key] = true

			// Verify the resource exists before adding
			obj, err := client.Resource(ref.GVR).Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
			if err != nil {
				// Resource doesn't exist in source -- skip silently
				continue
			}

			result = append(result, ref)

			// ConfigMaps, Secrets, PVCs, and SAs don't typically reference other resources,
			// but we still add them to the queue for completeness
			queue = append(queue, queueItem{obj: obj, ref: ref})
		}

		// Discover reverse references (Services, Ingresses, HPAs that point to this resource)
		reverseRefs, reverseObjs := discoverReverseRefs(ctx, client, current.obj, namespace)
		for i, ref := range reverseRefs {
			key := refKey{Resource: ref.GVR.Resource, Name: ref.Name, Namespace: ref.Namespace}
			if visited[key] {
				continue
			}
			visited[key] = true
			result = append(result, ref)

			// Continue traversal for reverse refs (e.g., Service -> Ingress chain)
			if reverseObjs[i] != nil {
				queue = append(queue, queueItem{obj: reverseObjs[i], ref: ref})
			}
		}
	}

	return result, nil
}

// discoverReverseRefs finds resources that depend on the given object:
// - Services whose selector matches the pod template labels
// - Ingresses whose backends reference those Services
// - HPAs that target this resource
func discoverReverseRefs(ctx context.Context, client dynamic.Interface, obj *unstructured.Unstructured, namespace string) ([]copier.ResourceRef, []*unstructured.Unstructured) {
	var refs []copier.ResourceRef
	var objs []*unstructured.Unstructured

	kind := obj.GetKind()

	// Services matching pod template labels (only for workload resources)
	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Pod":
		podLabels := extractPodTemplateLabels(obj)
		if len(podLabels) > 0 {
			svcRefs, svcObjs := findMatchingServices(ctx, client, namespace, podLabels)
			refs = append(refs, svcRefs...)
			objs = append(objs, svcObjs...)
		}
	}

	// Ingresses pointing to Services
	if kind == "Service" {
		ingRefs, ingObjs := findIngressesForService(ctx, client, namespace, obj.GetName())
		refs = append(refs, ingRefs...)
		objs = append(objs, ingObjs...)
	}

	// HPAs targeting this resource
	switch kind {
	case "Deployment", "StatefulSet", "ReplicaSet":
		hpaRefs, hpaObjs := findHPAsForResource(ctx, client, namespace, obj.GetKind(), obj.GetName())
		refs = append(refs, hpaRefs...)
		objs = append(objs, hpaObjs...)
	}

	return refs, objs
}

// findMatchingServices finds Services whose selector is a subset of the given labels.
func findMatchingServices(ctx context.Context, client dynamic.Interface, namespace string, podLabels map[string]string) ([]copier.ResourceRef, []*unstructured.Unstructured) {
	svcGVR := schema.GroupVersionResource{Version: "v1", Resource: "services"}
	svcList, err := client.Resource(svcGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil
	}

	var refs []copier.ResourceRef
	var objs []*unstructured.Unstructured

	for i := range svcList.Items {
		svc := &svcList.Items[i]
		spec, ok := svc.Object["spec"].(map[string]interface{})
		if !ok {
			continue
		}
		selectorRaw, ok := spec["selector"].(map[string]interface{})
		if !ok || len(selectorRaw) == 0 {
			continue
		}

		// Check if all selector labels match the pod template labels
		match := true
		for k, v := range selectorRaw {
			sv, ok := v.(string)
			if !ok {
				match = false
				break
			}
			if podLabels[k] != sv {
				match = false
				break
			}
		}

		if match {
			refs = append(refs, copier.ResourceRef{
				GVR:       svcGVR,
				Name:      svc.GetName(),
				Namespace: namespace,
			})
			objs = append(objs, svc)
		}
	}

	return refs, objs
}

// findIngressesForService finds Ingresses that reference the given Service.
func findIngressesForService(ctx context.Context, client dynamic.Interface, namespace, serviceName string) ([]copier.ResourceRef, []*unstructured.Unstructured) {
	ingGVR := schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}
	ingList, err := client.Resource(ingGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil
	}

	var refs []copier.ResourceRef
	var objs []*unstructured.Unstructured

	for i := range ingList.Items {
		ing := &ingList.Items[i]
		if ingressReferencesService(ing, serviceName) {
			refs = append(refs, copier.ResourceRef{
				GVR:       ingGVR,
				Name:      ing.GetName(),
				Namespace: namespace,
			})
			objs = append(objs, ing)
		}
	}

	return refs, objs
}

// ingressReferencesService checks if an Ingress has any backend referencing the named Service.
func ingressReferencesService(ing *unstructured.Unstructured, serviceName string) bool {
	spec, ok := ing.Object["spec"].(map[string]interface{})
	if !ok {
		return false
	}

	// Check default backend
	if db, ok := spec["defaultBackend"].(map[string]interface{}); ok {
		if svc, ok := db["service"].(map[string]interface{}); ok {
			if name, ok := svc["name"].(string); ok && name == serviceName {
				return true
			}
		}
	}

	// Check rules
	rules, ok := spec["rules"].([]interface{})
	if !ok {
		return false
	}
	for _, r := range rules {
		rule, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		http, ok := rule["http"].(map[string]interface{})
		if !ok {
			continue
		}
		paths, ok := http["paths"].([]interface{})
		if !ok {
			continue
		}
		for _, p := range paths {
			path, ok := p.(map[string]interface{})
			if !ok {
				continue
			}
			backend, ok := path["backend"].(map[string]interface{})
			if !ok {
				continue
			}
			if svc, ok := backend["service"].(map[string]interface{}); ok {
				if name, ok := svc["name"].(string); ok && name == serviceName {
					return true
				}
			}
		}
	}

	return false
}

// findHPAsForResource finds HPAs targeting the given resource.
func findHPAsForResource(ctx context.Context, client dynamic.Interface, namespace, kind, name string) ([]copier.ResourceRef, []*unstructured.Unstructured) {
	hpaGVR := schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}
	hpaList, err := client.Resource(hpaGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		// Try v1 if v2 is not available
		hpaGVR = schema.GroupVersionResource{Group: "autoscaling", Version: "v1", Resource: "horizontalpodautoscalers"}
		hpaList, err = client.Resource(hpaGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, nil
		}
	}

	var refs []copier.ResourceRef
	var objs []*unstructured.Unstructured

	for i := range hpaList.Items {
		hpa := &hpaList.Items[i]
		spec, ok := hpa.Object["spec"].(map[string]interface{})
		if !ok {
			continue
		}
		scaleRef, ok := spec["scaleTargetRef"].(map[string]interface{})
		if !ok {
			continue
		}
		refKind, _ := scaleRef["kind"].(string)
		refName, _ := scaleRef["name"].(string)
		if refKind == kind && refName == name {
			refs = append(refs, copier.ResourceRef{
				GVR:       hpaGVR,
				Name:      hpa.GetName(),
				Namespace: namespace,
			})
			objs = append(objs, hpa)
		}
	}

	return refs, objs
}
