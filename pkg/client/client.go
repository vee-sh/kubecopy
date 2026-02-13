package client

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
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
