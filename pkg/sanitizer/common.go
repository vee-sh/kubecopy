package sanitizer

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// SanitizeCommon strips metadata and fields that would cause conflicts when
// creating a copy of a Kubernetes resource. This is always applied to every resource.
func SanitizeCommon(obj *unstructured.Unstructured, targetNamespace, targetName string) []Warning {
	// ---- Strip server-set metadata ----
	stripMetadataFields := []string{
		"uid",
		"resourceVersion",
		"creationTimestamp",
		"generation",
		"selfLink",
		"managedFields",
	}
	metadata, ok := obj.Object["metadata"].(map[string]interface{})
	if !ok {
		return nil
	}

	for _, field := range stripMetadataFields {
		delete(metadata, field)
	}

	// Strip ownerReferences -- managed children are recreated by controllers
	delete(metadata, "ownerReferences")

	// Strip last-applied-configuration annotation
	if annotations, ok := metadata["annotations"].(map[string]interface{}); ok {
		delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
		// If annotations map is now empty, remove it entirely
		if len(annotations) == 0 {
			delete(metadata, "annotations")
		}
	}

	// ---- Strip status ----
	delete(obj.Object, "status")

	// ---- Rewrite namespace (empty for cluster-scoped resources) ----
	obj.SetNamespace(targetNamespace)

	// ---- Rewrite name ----
	if targetName != "" {
		obj.SetName(targetName)
	}

	return nil
}
