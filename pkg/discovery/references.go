package discovery

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/a13x22/kubecopy/pkg/copier"
)

// extractForwardRefs finds all resources that the given object depends on:
// ConfigMaps, Secrets, PVCs, and ServiceAccounts referenced in the pod spec.
func extractForwardRefs(obj *unstructured.Unstructured, namespace string) []copier.ResourceRef {
	var refs []copier.ResourceRef

	podSpec := extractPodSpec(obj)
	if podSpec == nil {
		return nil
	}

	// ConfigMaps
	for _, name := range extractConfigMapNames(podSpec) {
		refs = append(refs, copier.ResourceRef{
			GVR:       schema.GroupVersionResource{Version: "v1", Resource: "configmaps"},
			Name:      name,
			Namespace: namespace,
		})
	}

	// Secrets
	for _, name := range extractSecretNames(podSpec) {
		refs = append(refs, copier.ResourceRef{
			GVR:       schema.GroupVersionResource{Version: "v1", Resource: "secrets"},
			Name:      name,
			Namespace: namespace,
		})
	}

	// PVCs
	for _, name := range extractPVCNames(podSpec) {
		refs = append(refs, copier.ResourceRef{
			GVR:       schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"},
			Name:      name,
			Namespace: namespace,
		})
	}

	// ServiceAccount
	if sa := extractServiceAccountName(podSpec); sa != "" && sa != "default" {
		refs = append(refs, copier.ResourceRef{
			GVR:       schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"},
			Name:      sa,
			Namespace: namespace,
		})
	}

	return refs
}

// extractPodSpec navigates to the pod spec within various resource types.
func extractPodSpec(obj *unstructured.Unstructured) map[string]interface{} {
	kind := obj.GetKind()
	switch kind {
	case "Pod":
		spec, _ := obj.Object["spec"].(map[string]interface{})
		return spec
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet":
		return getPodSpecFromTemplate(obj.Object)
	case "Job":
		return getPodSpecFromTemplate(obj.Object)
	case "CronJob":
		spec, _ := obj.Object["spec"].(map[string]interface{})
		if spec == nil {
			return nil
		}
		jobTemplate, _ := spec["jobTemplate"].(map[string]interface{})
		if jobTemplate == nil {
			return nil
		}
		return getPodSpecFromTemplateMap(jobTemplate)
	}
	return nil
}

func getPodSpecFromTemplate(objMap map[string]interface{}) map[string]interface{} {
	spec, _ := objMap["spec"].(map[string]interface{})
	if spec == nil {
		return nil
	}
	return getPodSpecFromTemplateMap(spec)
}

func getPodSpecFromTemplateMap(spec map[string]interface{}) map[string]interface{} {
	template, _ := spec["template"].(map[string]interface{})
	if template == nil {
		return nil
	}
	podSpec, _ := template["spec"].(map[string]interface{})
	return podSpec
}

// extractPodTemplateLabels extracts the pod template labels from workload resources.
func extractPodTemplateLabels(obj *unstructured.Unstructured) map[string]string {
	kind := obj.GetKind()
	var template map[string]interface{}

	switch kind {
	case "Pod":
		labels := obj.GetLabels()
		return labels
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job":
		spec, _ := obj.Object["spec"].(map[string]interface{})
		if spec == nil {
			return nil
		}
		template, _ = spec["template"].(map[string]interface{})
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
		template, _ = jobSpec["template"].(map[string]interface{})
	}

	if template == nil {
		return nil
	}
	meta, _ := template["metadata"].(map[string]interface{})
	if meta == nil {
		return nil
	}
	labelsRaw, _ := meta["labels"].(map[string]interface{})
	if labelsRaw == nil {
		return nil
	}

	labels := make(map[string]string, len(labelsRaw))
	for k, v := range labelsRaw {
		if s, ok := v.(string); ok {
			labels[k] = s
		}
	}
	return labels
}

// ---- Name extractors for pod spec ----

func extractConfigMapNames(podSpec map[string]interface{}) []string {
	seen := map[string]bool{}
	var names []string

	if volumes, ok := podSpec["volumes"].([]interface{}); ok {
		for _, v := range volumes {
			vol, _ := v.(map[string]interface{})
			if vol == nil {
				continue
			}
			if cm, ok := vol["configMap"].(map[string]interface{}); ok {
				if name, ok := cm["name"].(string); ok && !seen[name] {
					seen[name] = true
					names = append(names, name)
				}
			}
			extractFromProjected(vol, "configMap", "name", seen, &names)
		}
	}

	extractFromContainerEnv(podSpec, "configMapRef", "name", "configMapKeyRef", "name", seen, &names)

	return names
}

func extractSecretNames(podSpec map[string]interface{}) []string {
	seen := map[string]bool{}
	var names []string

	if volumes, ok := podSpec["volumes"].([]interface{}); ok {
		for _, v := range volumes {
			vol, _ := v.(map[string]interface{})
			if vol == nil {
				continue
			}
			if secret, ok := vol["secret"].(map[string]interface{}); ok {
				if name, ok := secret["secretName"].(string); ok && !seen[name] {
					seen[name] = true
					names = append(names, name)
				}
			}
			extractFromProjected(vol, "secret", "name", seen, &names)
		}
	}

	extractFromContainerEnv(podSpec, "secretRef", "name", "secretKeyRef", "name", seen, &names)

	return names
}

func extractPVCNames(podSpec map[string]interface{}) []string {
	seen := map[string]bool{}
	var names []string

	if volumes, ok := podSpec["volumes"].([]interface{}); ok {
		for _, v := range volumes {
			vol, _ := v.(map[string]interface{})
			if vol == nil {
				continue
			}
			if pvc, ok := vol["persistentVolumeClaim"].(map[string]interface{}); ok {
				if name, ok := pvc["claimName"].(string); ok && !seen[name] {
					seen[name] = true
					names = append(names, name)
				}
			}
		}
	}

	return names
}

func extractServiceAccountName(podSpec map[string]interface{}) string {
	if sa, ok := podSpec["serviceAccountName"].(string); ok {
		return sa
	}
	if sa, ok := podSpec["serviceAccount"].(string); ok {
		return sa
	}
	return ""
}

// ---- Helpers ----

func extractFromProjected(vol map[string]interface{}, sourceKey, nameKey string, seen map[string]bool, names *[]string) {
	projected, ok := vol["projected"].(map[string]interface{})
	if !ok {
		return
	}
	sources, ok := projected["sources"].([]interface{})
	if !ok {
		return
	}
	for _, s := range sources {
		src, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		if ref, ok := src[sourceKey].(map[string]interface{}); ok {
			if name, ok := ref[nameKey].(string); ok && !seen[name] {
				seen[name] = true
				*names = append(*names, name)
			}
		}
	}
}

func extractFromContainerEnv(podSpec map[string]interface{}, envFromRefKey, envFromNameKey, envVarRefKey, envVarNameKey string, seen map[string]bool, names *[]string) {
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
					if ref, ok := entry[envFromRefKey].(map[string]interface{}); ok {
						if name, ok := ref[envFromNameKey].(string); ok && !seen[name] {
							seen[name] = true
							*names = append(*names, name)
						}
					}
				}
			}
			// env[].valueFrom
			if envVars, ok := container["env"].([]interface{}); ok {
				for _, ev := range envVars {
					envVar, ok := ev.(map[string]interface{})
					if !ok {
						continue
					}
					if vf, ok := envVar["valueFrom"].(map[string]interface{}); ok {
						if ref, ok := vf[envVarRefKey].(map[string]interface{}); ok {
							if name, ok := ref[envVarNameKey].(string); ok && !seen[name] {
								seen[name] = true
								*names = append(*names, name)
							}
						}
					}
				}
			}
		}
	}
}
