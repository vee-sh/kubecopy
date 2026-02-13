package sanitizer

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func init() {
	Register("PersistentVolumeClaim", SanitizerFunc(sanitizePVC))
}

func sanitizePVC(obj *unstructured.Unstructured) []Warning {
	var warnings []Warning
	identifier := fmt.Sprintf("PersistentVolumeClaim/%s", obj.GetName())

	spec, ok := obj.Object["spec"].(map[string]interface{})
	if !ok {
		return nil
	}

	// Remove volumeName (PV binding) so a new PV can be dynamically provisioned
	if volumeName, ok := spec["volumeName"].(string); ok && volumeName != "" {
		delete(spec, "volumeName")
		warnings = append(warnings, Warning{
			Resource: identifier,
			Message:  fmt.Sprintf("removed volumeName %q (PV binding) to allow dynamic provisioning", volumeName),
		})
	}

	// Remove PV binding annotations
	annotations := obj.GetAnnotations()
	if annotations != nil {
		pvAnnotations := []string{
			"pv.kubernetes.io/bind-completed",
			"pv.kubernetes.io/bound-by-controller",
			"volume.beta.kubernetes.io/storage-provisioner",
			"volume.kubernetes.io/storage-provisioner",
			"volume.kubernetes.io/selected-node",
		}
		changed := false
		for _, ann := range pvAnnotations {
			if _, ok := annotations[ann]; ok {
				delete(annotations, ann)
				changed = true
			}
		}
		if changed {
			if len(annotations) == 0 {
				obj.SetAnnotations(nil)
			} else {
				obj.SetAnnotations(annotations)
			}
			warnings = append(warnings, Warning{
				Resource: identifier,
				Message:  "removed PV binding annotations",
			})
		}
	}

	return warnings
}
