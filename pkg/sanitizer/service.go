package sanitizer

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func init() {
	Register("Service", SanitizerFunc(sanitizeService))
}

func sanitizeService(obj *unstructured.Unstructured) []Warning {
	var warnings []Warning
	identifier := fmt.Sprintf("Service/%s", obj.GetName())

	spec, ok := obj.Object["spec"].(map[string]interface{})
	if !ok {
		return nil
	}

	// Reset clusterIP to let the API server assign a new one
	if clusterIP, ok := spec["clusterIP"].(string); ok && clusterIP != "" && clusterIP != "None" {
		spec["clusterIP"] = ""
		warnings = append(warnings, Warning{
			Resource: identifier,
			Message:  fmt.Sprintf("reset clusterIP (was %s) to let the cluster assign a new one", clusterIP),
		})
	}

	// Reset clusterIPs
	if clusterIPs, ok := spec["clusterIPs"].([]interface{}); ok && len(clusterIPs) > 0 {
		// Keep "None" for headless services
		if len(clusterIPs) == 1 {
			if ip, ok := clusterIPs[0].(string); ok && ip == "None" {
				// Headless service -- keep it
				goto skipClusterIPs
			}
		}
		spec["clusterIPs"] = []interface{}{}
	}
skipClusterIPs:

	// Clear nodePorts from each port entry
	if ports, ok := spec["ports"].([]interface{}); ok {
		for _, p := range ports {
			port, ok := p.(map[string]interface{})
			if !ok {
				continue
			}
			if np, exists := port["nodePort"]; exists {
				delete(port, "nodePort")
				warnings = append(warnings, Warning{
					Resource: identifier,
					Message:  fmt.Sprintf("removed nodePort %v to let the cluster assign a new one", np),
				})
			}
		}
	}

	// Warn if loadBalancerIP is set
	if lbIP, ok := spec["loadBalancerIP"].(string); ok && lbIP != "" {
		warnings = append(warnings, Warning{
			Resource: identifier,
			Message:  fmt.Sprintf("loadBalancerIP is set to %s -- this may conflict in the target cluster", lbIP),
		})
	}

	// Warn on ExternalName type
	if svcType, ok := spec["type"].(string); ok && svcType == "ExternalName" {
		warnings = append(warnings, Warning{
			Resource: identifier,
			Message:  "ExternalName service -- verify the external name is valid in the target",
		})
	}

	// Remove healthCheckNodePort (auto-assigned for LoadBalancer + externalTrafficPolicy: Local)
	delete(spec, "healthCheckNodePort")

	return warnings
}
