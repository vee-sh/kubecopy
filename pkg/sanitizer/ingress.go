package sanitizer

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func init() {
	Register("Ingress", SanitizerFunc(sanitizeIngress))
}

func sanitizeIngress(obj *unstructured.Unstructured) []Warning {
	var warnings []Warning
	identifier := fmt.Sprintf("Ingress/%s", obj.GetName())

	spec, ok := obj.Object["spec"].(map[string]interface{})
	if !ok {
		return nil
	}

	// Warn about hardcoded hostnames that may conflict
	rules, ok := spec["rules"].([]interface{})
	if !ok {
		return nil
	}

	for _, r := range rules {
		rule, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		if host, ok := rule["host"].(string); ok && host != "" {
			warnings = append(warnings, Warning{
				Resource: identifier,
				Message:  fmt.Sprintf("ingress rule has hardcoded host %q -- this may conflict if the same hostname is already used in the target", host),
			})
		}
	}

	// Warn about TLS hostnames
	if tls, ok := spec["tls"].([]interface{}); ok {
		for _, t := range tls {
			tlsEntry, ok := t.(map[string]interface{})
			if !ok {
				continue
			}
			if hosts, ok := tlsEntry["hosts"].([]interface{}); ok {
				for _, h := range hosts {
					if host, ok := h.(string); ok {
						warnings = append(warnings, Warning{
							Resource: identifier,
							Message:  fmt.Sprintf("TLS entry references host %q -- verify the TLS secret and DNS are valid in the target", host),
						})
					}
				}
			}
		}
	}

	return warnings
}
