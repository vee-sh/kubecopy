package client

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

// Clients holds dynamic clients and REST mappers for source and target clusters.
type Clients struct {
	SourceDynamic dynamic.Interface
	SourceMapper  meta.RESTMapper

	TargetDynamic dynamic.Interface
	TargetMapper  meta.RESTMapper
}

// New creates Clients from the given kubeconfig parameters.
// sourceContext uses the current kubeconfig/context. Target parameters
// allow pointing to a different context/kubeconfig for cross-cluster copies.
func New(kubeconfig, sourceContext, targetKubeconfig, targetContext string) (*Clients, error) {
	sourceCfg, err := buildConfig(kubeconfig, sourceContext)
	if err != nil {
		return nil, fmt.Errorf("source cluster config: %w", err)
	}

	// Determine target config: use target overrides if provided, otherwise same as source.
	var targetCfg *rest.Config
	if targetKubeconfig != "" || targetContext != "" {
		kc := kubeconfig
		if targetKubeconfig != "" {
			kc = targetKubeconfig
		}
		ctx := sourceContext
		if targetContext != "" {
			ctx = targetContext
		}
		targetCfg, err = buildConfig(kc, ctx)
		if err != nil {
			return nil, fmt.Errorf("target cluster config: %w", err)
		}
	} else {
		targetCfg = sourceCfg
	}

	srcDyn, err := dynamic.NewForConfig(sourceCfg)
	if err != nil {
		return nil, fmt.Errorf("source dynamic client: %w", err)
	}

	srcMapper, err := buildMapper(sourceCfg)
	if err != nil {
		return nil, fmt.Errorf("source REST mapper: %w", err)
	}

	tgtDyn, err := dynamic.NewForConfig(targetCfg)
	if err != nil {
		return nil, fmt.Errorf("target dynamic client: %w", err)
	}

	tgtMapper, err := buildMapper(targetCfg)
	if err != nil {
		return nil, fmt.Errorf("target REST mapper: %w", err)
	}

	return &Clients{
		SourceDynamic: srcDyn,
		SourceMapper:  srcMapper,
		TargetDynamic: tgtDyn,
		TargetMapper:  tgtMapper,
	}, nil
}

func buildConfig(kubeconfig, context string) (*rest.Config, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		rules.ExplicitPath = kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if context != "" {
		overrides.CurrentContext = context
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
}

func buildMapper(cfg *rest.Config) (meta.RESTMapper, error) {
	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, err
	}
	groups, err := restmapper.GetAPIGroupResources(dc)
	if err != nil {
		return nil, err
	}
	return restmapper.NewDiscoveryRESTMapper(groups), nil
}

// ResolvedResource holds a resolved GVR and the proper Kind name from the API server.
type ResolvedResource struct {
	GVR  schema.GroupVersionResource
	Kind string // e.g. "Deployment", "Service" -- from the API server
}

// Resolve takes a user-provided resource string (e.g. "deployment", "deploy",
// "deployments", "deployments.apps") and resolves it against the source cluster's
// API discovery, just like kubectl does. Returns the GVR and proper Kind name.
func (c *Clients) Resolve(resource string) (ResolvedResource, error) {
	// The REST mapper handles all the heavy lifting:
	// - plural/singular ("deployment" / "deployments")
	// - short names ("deploy", "svc", "cm", "po", etc.)
	// - resource.group format ("deployments.apps")
	// - CRDs and any other API-server-registered resource
	gvr, err := resolveGVR(c.SourceMapper, resource)
	if err != nil {
		return ResolvedResource{}, fmt.Errorf("cannot resolve resource type %q: %w\n    Run 'kubectl api-resources' to see available types.", resource, err)
	}

	// Get the Kind name from the mapper
	kind := kindForGVR(c.SourceMapper, gvr)

	return ResolvedResource{GVR: gvr, Kind: kind}, nil
}

// resolveGVR uses the REST mapper to convert a user-provided resource string
// to a fully qualified GroupVersionResource.
func resolveGVR(mapper meta.RESTMapper, resource string) (schema.GroupVersionResource, error) {
	// Try as a fully qualified resource first (handles "deployments.apps" format)
	fullySpecifiedGVR, groupResource := schema.ParseResourceArg(resource)
	if fullySpecifiedGVR != nil {
		// Validate it exists
		if _, err := mapper.RESTMapping(schema.GroupKind{Group: fullySpecifiedGVR.Group, Kind: ""}, fullySpecifiedGVR.Version); err == nil {
			return *fullySpecifiedGVR, nil
		}
	}

	// Use the mapper to resolve short names, plural, singular
	gvr, err := mapper.ResourceFor(groupResource.WithVersion(""))
	if err != nil {
		return schema.GroupVersionResource{}, err
	}

	return gvr, nil
}

// kindForGVR looks up the Kind string for a GVR from the REST mapper.
func kindForGVR(mapper meta.RESTMapper, gvr schema.GroupVersionResource) string {
	gvk, err := mapper.KindFor(gvr)
	if err != nil {
		// Fallback: capitalize the resource name
		r := gvr.Resource
		if len(r) > 0 {
			return string(r[0]-32) + r[1:]
		}
		return r
	}
	return gvk.Kind
}
