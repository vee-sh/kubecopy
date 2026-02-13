package sanitizer

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func init() {
	Register("ServiceAccount", SanitizerFunc(sanitizeServiceAccount))
}

func sanitizeServiceAccount(obj *unstructured.Unstructured) []Warning {
	var warnings []Warning
	identifier := fmt.Sprintf("ServiceAccount/%s", obj.GetName())

	// Remove auto-generated secrets (token secrets created by the token controller)
	if secrets, ok := obj.Object["secrets"].([]interface{}); ok && len(secrets) > 0 {
		var cleanSecrets []interface{}
		for _, s := range secrets {
			secret, ok := s.(map[string]interface{})
			if !ok {
				cleanSecrets = append(cleanSecrets, s)
				continue
			}
			name, _ := secret["name"].(string)
			// Auto-generated token secrets follow the pattern "<sa-name>-token-xxxxx"
			if strings.Contains(name, "-token-") {
				warnings = append(warnings, Warning{
					Resource: identifier,
					Message:  fmt.Sprintf("removed auto-generated token secret reference %q", name),
				})
				continue
			}
			cleanSecrets = append(cleanSecrets, s)
		}
		if len(cleanSecrets) == 0 {
			delete(obj.Object, "secrets")
		} else {
			obj.Object["secrets"] = cleanSecrets
		}
	}

	// Remove imagePullSecrets that reference auto-generated secrets
	if ips, ok := obj.Object["imagePullSecrets"].([]interface{}); ok {
		var cleanIPS []interface{}
		for _, s := range ips {
			secret, ok := s.(map[string]interface{})
			if !ok {
				cleanIPS = append(cleanIPS, s)
				continue
			}
			name, _ := secret["name"].(string)
			if strings.Contains(name, "-dockercfg-") {
				warnings = append(warnings, Warning{
					Resource: identifier,
					Message:  fmt.Sprintf("removed auto-generated imagePullSecret reference %q", name),
				})
				continue
			}
			cleanIPS = append(cleanIPS, s)
		}
		if len(cleanIPS) == 0 {
			delete(obj.Object, "imagePullSecrets")
		} else {
			obj.Object["imagePullSecrets"] = cleanIPS
		}
	}

	return warnings
}
