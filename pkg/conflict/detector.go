package conflict

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// Type classifies a conflict.
type Type string

const (
	TypeExistence Type = "existence" // resource already exists in target
	TypeAddress   Type = "address"   // hardcoded network address conflict
	TypeReference Type = "reference" // missing referenced resource in target
)

// Conflict describes a single detected conflict.
type Conflict struct {
	Type     Type
	Resource string // e.g. "Service/my-svc"
	Message  string
}

// Detect runs all pre-flight conflict checks for a resource about to be created.
func Detect(ctx context.Context, targetClient dynamic.Interface, gvr schema.GroupVersionResource, obj *unstructured.Unstructured, targetNS string) []Conflict {
	var conflicts []Conflict

	name := obj.GetName()
	identifier := fmt.Sprintf("%s/%s", obj.GetKind(), name)

	// 1. Existence check
	_, err := targetClient.Resource(gvr).Namespace(targetNS).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		conflicts = append(conflicts, Conflict{
			Type:     TypeExistence,
			Resource: identifier,
			Message:  fmt.Sprintf("%s already exists in namespace %q", identifier, targetNS),
		})
	}

	// 2. Address conflicts (resource-specific)
	conflicts = append(conflicts, detectAddressConflicts(obj)...)

	// 3. Reference conflicts
	conflicts = append(conflicts, detectReferenceConflicts(ctx, targetClient, obj, targetNS)...)

	return conflicts
}

// detectAddressConflicts checks for hardcoded network addresses that would conflict.
func detectAddressConflicts(obj *unstructured.Unstructured) []Conflict {
	kind := obj.GetKind()
	switch kind {
	case "Service":
		return detectServiceAddressConflicts(obj)
	default:
		return nil
	}
}

// detectServiceAddressConflicts checks if a Service still has hardcoded addresses
// after sanitization (which should have cleared them, but we double-check).
func detectServiceAddressConflicts(obj *unstructured.Unstructured) []Conflict {
	var conflicts []Conflict
	identifier := fmt.Sprintf("Service/%s", obj.GetName())

	spec, ok := obj.Object["spec"].(map[string]interface{})
	if !ok {
		return nil
	}

	// Check for residual clusterIP (should have been cleared by sanitizer)
	if clusterIP, ok := spec["clusterIP"].(string); ok && clusterIP != "" && clusterIP != "None" {
		conflicts = append(conflicts, Conflict{
			Type:     TypeAddress,
			Resource: identifier,
			Message:  fmt.Sprintf("Service has hardcoded clusterIP %s that may conflict", clusterIP),
		})
	}

	// Check for hardcoded nodePorts
	if ports, ok := spec["ports"].([]interface{}); ok {
		for _, p := range ports {
			port, ok := p.(map[string]interface{})
			if !ok {
				continue
			}
			if np, ok := port["nodePort"]; ok {
				if npVal, ok := toInt64(np); ok && npVal > 0 {
					conflicts = append(conflicts, Conflict{
						Type:     TypeAddress,
						Resource: identifier,
						Message:  fmt.Sprintf("Service has hardcoded nodePort %d that may conflict", npVal),
					})
				}
			}
		}
	}

	// Check for loadBalancerIP
	if lbIP, ok := spec["loadBalancerIP"].(string); ok && lbIP != "" {
		conflicts = append(conflicts, Conflict{
			Type:     TypeAddress,
			Resource: identifier,
			Message:  fmt.Sprintf("Service has hardcoded loadBalancerIP %s that may conflict", lbIP),
		})
	}

	return conflicts
}

// detectReferenceConflicts checks whether resources referenced by the object
// exist in the target namespace/cluster.
func detectReferenceConflicts(ctx context.Context, targetClient dynamic.Interface, obj *unstructured.Unstructured, targetNS string) []Conflict {
	var conflicts []Conflict
	identifier := fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName())

	// Extract pod spec (works for Deployment, StatefulSet, DaemonSet, Job, Pod, etc.)
	podSpec := extractPodSpec(obj)
	if podSpec == nil {
		return nil
	}

	// Check ConfigMap references
	for _, cmName := range extractConfigMapRefs(podSpec) {
		if !resourceExists(ctx, targetClient, schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, cmName, targetNS) {
			conflicts = append(conflicts, Conflict{
				Type:     TypeReference,
				Resource: identifier,
				Message:  fmt.Sprintf("references ConfigMap %q which does not exist in target namespace %q (consider --recursive)", cmName, targetNS),
			})
		}
	}

	// Check Secret references
	for _, secretName := range extractSecretRefs(podSpec) {
		if !resourceExists(ctx, targetClient, schema.GroupVersionResource{Version: "v1", Resource: "secrets"}, secretName, targetNS) {
			conflicts = append(conflicts, Conflict{
				Type:     TypeReference,
				Resource: identifier,
				Message:  fmt.Sprintf("references Secret %q which does not exist in target namespace %q (consider --recursive)", secretName, targetNS),
			})
		}
	}

	// Check PVC references
	for _, pvcName := range extractPVCRefs(podSpec) {
		if !resourceExists(ctx, targetClient, schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}, pvcName, targetNS) {
			conflicts = append(conflicts, Conflict{
				Type:     TypeReference,
				Resource: identifier,
				Message:  fmt.Sprintf("references PVC %q which does not exist in target namespace %q (consider --recursive)", pvcName, targetNS),
			})
		}
	}

	// Check ServiceAccount references
	if saName := extractServiceAccountRef(podSpec); saName != "" && saName != "default" {
		if !resourceExists(ctx, targetClient, schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"}, saName, targetNS) {
			conflicts = append(conflicts, Conflict{
				Type:     TypeReference,
				Resource: identifier,
				Message:  fmt.Sprintf("references ServiceAccount %q which does not exist in target namespace %q (consider --recursive)", saName, targetNS),
			})
		}
	}

	return conflicts
}

// extractPodSpec navigates to the pod spec within various resource types.
func extractPodSpec(obj *unstructured.Unstructured) map[string]interface{} {
	kind := obj.GetKind()
	switch kind {
	case "Pod":
		spec, _ := obj.Object["spec"].(map[string]interface{})
		return spec
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet":
		spec, _ := obj.Object["spec"].(map[string]interface{})
		if spec == nil {
			return nil
		}
		template, _ := spec["template"].(map[string]interface{})
		if template == nil {
			return nil
		}
		podSpec, _ := template["spec"].(map[string]interface{})
		return podSpec
	case "Job":
		spec, _ := obj.Object["spec"].(map[string]interface{})
		if spec == nil {
			return nil
		}
		template, _ := spec["template"].(map[string]interface{})
		if template == nil {
			return nil
		}
		podSpec, _ := template["spec"].(map[string]interface{})
		return podSpec
	case "CronJob":
		spec, _ := obj.Object["spec"].(map[string]interface{})
		if spec == nil {
			return nil
		}
		jobTemplate, _ := spec["jobTemplate"].(map[string]interface{})
		if jobTemplate == nil {
			return nil
		}
		jobSpec, _ := jobTemplate["spec"].(map[string]interface{})
		if jobSpec == nil {
			return nil
		}
		template, _ := jobSpec["template"].(map[string]interface{})
		if template == nil {
			return nil
		}
		podSpec, _ := template["spec"].(map[string]interface{})
		return podSpec
	}
	return nil
}

// extractConfigMapRefs extracts all ConfigMap names referenced in a pod spec.
func extractConfigMapRefs(podSpec map[string]interface{}) []string {
	seen := map[string]bool{}
	var refs []string

	// From volumes
	if volumes, ok := podSpec["volumes"].([]interface{}); ok {
		for _, v := range volumes {
			vol, ok := v.(map[string]interface{})
			if !ok {
				continue
			}
			if cm, ok := vol["configMap"].(map[string]interface{}); ok {
				if name, ok := cm["name"].(string); ok && !seen[name] {
					seen[name] = true
					refs = append(refs, name)
				}
			}
			if projected, ok := vol["projected"].(map[string]interface{}); ok {
				if sources, ok := projected["sources"].([]interface{}); ok {
					for _, s := range sources {
						src, ok := s.(map[string]interface{})
						if !ok {
							continue
						}
						if cm, ok := src["configMap"].(map[string]interface{}); ok {
							if name, ok := cm["name"].(string); ok && !seen[name] {
								seen[name] = true
								refs = append(refs, name)
							}
						}
					}
				}
			}
		}
	}

	// From envFrom and env.valueFrom in containers
	for _, containerField := range []string{"containers", "initContainers"} {
		containers, ok := podSpec[containerField].([]interface{})
		if !ok {
			continue
		}
		for _, c := range containers {
			container, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			// envFrom
			if envFrom, ok := container["envFrom"].([]interface{}); ok {
				for _, ef := range envFrom {
					entry, ok := ef.(map[string]interface{})
					if !ok {
						continue
					}
					if cmRef, ok := entry["configMapRef"].(map[string]interface{}); ok {
						if name, ok := cmRef["name"].(string); ok && !seen[name] {
							seen[name] = true
							refs = append(refs, name)
						}
					}
				}
			}
			// env[].valueFrom.configMapKeyRef
			if envVars, ok := container["env"].([]interface{}); ok {
				for _, ev := range envVars {
					envVar, ok := ev.(map[string]interface{})
					if !ok {
						continue
					}
					if vf, ok := envVar["valueFrom"].(map[string]interface{}); ok {
						if cmRef, ok := vf["configMapKeyRef"].(map[string]interface{}); ok {
							if name, ok := cmRef["name"].(string); ok && !seen[name] {
								seen[name] = true
								refs = append(refs, name)
							}
						}
					}
				}
			}
		}
	}

	return refs
}

// extractSecretRefs extracts all Secret names referenced in a pod spec.
func extractSecretRefs(podSpec map[string]interface{}) []string {
	seen := map[string]bool{}
	var refs []string

	// From volumes
	if volumes, ok := podSpec["volumes"].([]interface{}); ok {
		for _, v := range volumes {
			vol, ok := v.(map[string]interface{})
			if !ok {
				continue
			}
			if secret, ok := vol["secret"].(map[string]interface{}); ok {
				if name, ok := secret["secretName"].(string); ok && !seen[name] {
					seen[name] = true
					refs = append(refs, name)
				}
			}
			if projected, ok := vol["projected"].(map[string]interface{}); ok {
				if sources, ok := projected["sources"].([]interface{}); ok {
					for _, s := range sources {
						src, ok := s.(map[string]interface{})
						if !ok {
							continue
						}
						if secret, ok := src["secret"].(map[string]interface{}); ok {
							if name, ok := secret["name"].(string); ok && !seen[name] {
								seen[name] = true
								refs = append(refs, name)
							}
						}
					}
				}
			}
		}
	}

	// From envFrom and env.valueFrom in containers
	for _, containerField := range []string{"containers", "initContainers"} {
		containers, ok := podSpec[containerField].([]interface{})
		if !ok {
			continue
		}
		for _, c := range containers {
			container, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			if envFrom, ok := container["envFrom"].([]interface{}); ok {
				for _, ef := range envFrom {
					entry, ok := ef.(map[string]interface{})
					if !ok {
						continue
					}
					if secRef, ok := entry["secretRef"].(map[string]interface{}); ok {
						if name, ok := secRef["name"].(string); ok && !seen[name] {
							seen[name] = true
							refs = append(refs, name)
						}
					}
				}
			}
			if envVars, ok := container["env"].([]interface{}); ok {
				for _, ev := range envVars {
					envVar, ok := ev.(map[string]interface{})
					if !ok {
						continue
					}
					if vf, ok := envVar["valueFrom"].(map[string]interface{}); ok {
						if secRef, ok := vf["secretKeyRef"].(map[string]interface{}); ok {
							if name, ok := secRef["name"].(string); ok && !seen[name] {
								seen[name] = true
								refs = append(refs, name)
							}
						}
					}
				}
			}
		}
	}

	return refs
}

// extractPVCRefs extracts all PVC names referenced in a pod spec.
func extractPVCRefs(podSpec map[string]interface{}) []string {
	var refs []string
	seen := map[string]bool{}

	if volumes, ok := podSpec["volumes"].([]interface{}); ok {
		for _, v := range volumes {
			vol, ok := v.(map[string]interface{})
			if !ok {
				continue
			}
			if pvc, ok := vol["persistentVolumeClaim"].(map[string]interface{}); ok {
				if name, ok := pvc["claimName"].(string); ok && !seen[name] {
					seen[name] = true
					refs = append(refs, name)
				}
			}
		}
	}

	return refs
}

// extractServiceAccountRef extracts the ServiceAccount name from a pod spec.
func extractServiceAccountRef(podSpec map[string]interface{}) string {
	if sa, ok := podSpec["serviceAccountName"].(string); ok {
		return sa
	}
	// Deprecated field
	if sa, ok := podSpec["serviceAccount"].(string); ok {
		return sa
	}
	return ""
}

// resourceExists checks if a resource exists in the target namespace.
func resourceExists(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource, name, namespace string) bool {
	_, err := client.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	return err == nil
}

// toInt64 converts a numeric interface to int64.
func toInt64(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	}
	return 0, false
}
