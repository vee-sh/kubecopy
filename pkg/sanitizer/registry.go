package sanitizer

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Warning represents an advisory message produced during sanitization.
type Warning struct {
	Resource string // e.g. "Service/my-svc"
	Message  string
}

// Sanitizer transforms a resource to make it safe to create in the target.
type Sanitizer interface {
	// Sanitize modifies the object in place and returns any warnings.
	Sanitize(obj *unstructured.Unstructured) []Warning
}

// SanitizerFunc is an adapter to use ordinary functions as Sanitizers.
type SanitizerFunc func(obj *unstructured.Unstructured) []Warning

func (f SanitizerFunc) Sanitize(obj *unstructured.Unstructured) []Warning {
	return f(obj)
}

// Registry maps resource kinds to their specific sanitizers.
// The common sanitizer is always applied first.
var Registry = map[string]Sanitizer{}

// Register adds a resource-specific sanitizer for the given kind (lowercase).
func Register(kind string, s Sanitizer) {
	Registry[kind] = s
}

// Run applies the common sanitizer followed by any resource-specific sanitizer.
// Returns collected warnings.
func Run(obj *unstructured.Unstructured, targetNamespace, targetName string) []Warning {
	var warnings []Warning

	// Always apply universal sanitization
	warnings = append(warnings, SanitizeCommon(obj, targetNamespace, targetName)...)

	// Apply resource-specific sanitizer if registered
	kind := obj.GetKind()
	if s, ok := Registry[kind]; ok {
		warnings = append(warnings, s.Sanitize(obj)...)
	}

	return warnings
}
