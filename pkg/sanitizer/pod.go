package sanitizer

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func init() {
	Register("Pod", SanitizerFunc(sanitizePod))
}

func sanitizePod(obj *unstructured.Unstructured) []Warning {
	var warnings []Warning
	identifier := fmt.Sprintf("Pod/%s", obj.GetName())

	spec, ok := obj.Object["spec"].(map[string]interface{})
	if !ok {
		return nil
	}

	// Remove nodeName (scheduling assignment)
	if nodeName, ok := spec["nodeName"].(string); ok && nodeName != "" {
		delete(spec, "nodeName")
		warnings = append(warnings, Warning{
			Resource: identifier,
			Message:  fmt.Sprintf("removed nodeName %q to allow scheduler to place the pod", nodeName),
		})
	}

	// Remove auto-injected service account token volumes and volume mounts
	sanitizeSATokenVolumes(spec, identifier, &warnings)

	return warnings
}

// sanitizeSATokenVolumes removes the auto-injected service account token projected
// volumes and their corresponding volume mounts from the pod spec.
func sanitizeSATokenVolumes(spec map[string]interface{}, identifier string, warnings *[]Warning) {
	volumes, ok := spec["volumes"].([]interface{})
	if !ok {
		return
	}

	var cleanVolumes []interface{}
	removedNames := map[string]bool{}

	for _, v := range volumes {
		vol, ok := v.(map[string]interface{})
		if !ok {
			cleanVolumes = append(cleanVolumes, v)
			continue
		}
		name, _ := vol["name"].(string)
		// Auto-injected SA token volumes have names like "kube-api-access-xxxxx"
		if strings.HasPrefix(name, "kube-api-access-") {
			removedNames[name] = true
			*warnings = append(*warnings, Warning{
				Resource: identifier,
				Message:  fmt.Sprintf("removed auto-injected volume %q", name),
			})
			continue
		}
		cleanVolumes = append(cleanVolumes, v)
	}

	if len(removedNames) > 0 {
		spec["volumes"] = cleanVolumes

		// Also remove corresponding volumeMounts from all containers
		for _, containerField := range []string{"containers", "initContainers"} {
			containers, ok := spec[containerField].([]interface{})
			if !ok {
				continue
			}
			for _, c := range containers {
				container, ok := c.(map[string]interface{})
				if !ok {
					continue
				}
				mounts, ok := container["volumeMounts"].([]interface{})
				if !ok {
					continue
				}
				var cleanMounts []interface{}
				for _, m := range mounts {
					mount, ok := m.(map[string]interface{})
					if !ok {
						cleanMounts = append(cleanMounts, m)
						continue
					}
					mountName, _ := mount["name"].(string)
					if removedNames[mountName] {
						continue
					}
					cleanMounts = append(cleanMounts, m)
				}
				container["volumeMounts"] = cleanMounts
			}
		}
	}
}
