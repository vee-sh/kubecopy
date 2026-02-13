package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/a13x22/kubecopy/pkg/client"
	"github.com/a13x22/kubecopy/pkg/copier"
	"github.com/a13x22/kubecopy/pkg/discovery"
	"github.com/a13x22/kubecopy/pkg/output"
)

// Options holds all flags and parsed arguments for the copy command.
type Options struct {
	// Source identification
	SourceKubeconfig string
	SourceContext    string
	SourceNamespace  string
	ResourceArg     string // raw argument like "deployment/myapp"

	// Parsed from ResourceArg
	ResourceKind string
	ResourceName string

	// Target overrides
	ToNamespace  string
	ToName       string
	ToContext    string
	ToKubeconfig string

	// Behavior flags
	Recursive  bool
	DryRun     bool
	OnConflict string // "skip", "warn", "overwrite"
	Output     string // "table", "yaml", "json"
}

// NewCopyCommand creates the root cobra command for kubectl-copy.
func NewCopyCommand() *cobra.Command {
	o := &Options{}

	cmd := &cobra.Command{
		Use:   "copy <resource>/<name> [flags]",
		Short: "Copy Kubernetes resources across namespaces or clusters",
		Long: `Copy Kubernetes resources intelligently, sanitizing metadata and
detecting conflicts to avoid broken or duplicate resources.

Supports copying single resources or entire dependency graphs with --recursive.
Works across namespaces (same cluster) and across clusters (different context/kubeconfig).

Resource can be specified as:
  deployment/myapp
  deployment.apps/myapp
  deploy/myapp`,
		Example: `  # Copy a deployment to another namespace
  kubectl copy deployment/myapp --to-namespace staging

  # Copy with a new name in same namespace
  kubectl copy deployment/myapp --to-name myapp-v2

  # Copy to another cluster
  kubectl copy deployment/myapp --to-context prod-cluster --to-namespace default

  # Recursive copy (includes related ConfigMaps, Secrets, Services, etc.)
  kubectl copy deployment/myapp --to-namespace staging -r

  # Dry-run to preview what would happen
  kubectl copy deployment/myapp --to-namespace staging -r --dry-run`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ExactArgs(1),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return o.Complete(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.Run()
		},
	}

	// Source flags (standard kubectl flags)
	cmd.Flags().StringVar(&o.SourceKubeconfig, "kubeconfig", "", "path to the kubeconfig file")
	cmd.Flags().StringVar(&o.SourceContext, "context", "", "kubeconfig context to use for the source")
	cmd.Flags().StringVarP(&o.SourceNamespace, "namespace", "n", "", "source namespace (defaults to current context namespace)")

	// Target flags
	cmd.Flags().StringVar(&o.ToNamespace, "to-namespace", "", "target namespace (defaults to source namespace)")
	cmd.Flags().StringVar(&o.ToNamespace, "to-ns", "", "target namespace (alias for --to-namespace)")
	cmd.Flags().StringVar(&o.ToName, "to-name", "", "new resource name (required for same-namespace copy)")
	cmd.Flags().StringVar(&o.ToContext, "to-context", "", "target kubeconfig context (for cross-cluster copy)")
	cmd.Flags().StringVar(&o.ToKubeconfig, "to-kubeconfig", "", "target kubeconfig file (for cross-cluster copy)")

	// Behavior flags
	cmd.Flags().BoolVarP(&o.Recursive, "recursive", "r", false, "copy the full dependency graph")
	cmd.Flags().BoolVar(&o.DryRun, "dry-run", false, "preview what would be copied without making changes")
	cmd.Flags().StringVar(&o.OnConflict, "on-conflict", "skip", "conflict strategy: skip, warn, overwrite")
	cmd.Flags().StringVarP(&o.Output, "output", "o", "table", "dry-run output format: table, yaml, json")

	return cmd
}

// Complete parses and validates the command arguments.
func (o *Options) Complete(cmd *cobra.Command, args []string) error {
	o.ResourceArg = args[0]

	// Parse resource/name -- supports:
	//   deployment/myapp
	//   deployment.apps/myapp    (kubectl get-style with API group)
	//   deployments.apps/myapp
	parts := strings.SplitN(o.ResourceArg, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("invalid resource argument %q: expected <resource>/<name>", o.ResourceArg)
	}

	// Strip the API group suffix if present (e.g. "deployment.apps" -> "deployment")
	kindPart := strings.ToLower(parts[0])
	if dotIdx := strings.Index(kindPart, "."); dotIdx > 0 {
		kindPart = kindPart[:dotIdx]
	}
	o.ResourceKind = kindPart
	o.ResourceName = parts[1]

	// Default source namespace
	if o.SourceNamespace == "" {
		o.SourceNamespace = getDefaultNamespace(o.SourceKubeconfig, o.SourceContext)
	}

	// Default target namespace to source namespace
	if o.ToNamespace == "" {
		o.ToNamespace = o.SourceNamespace
	}

	// Validate: same namespace + no rename = conflict
	if o.ToNamespace == o.SourceNamespace && o.ToName == "" && o.ToContext == "" && o.ToKubeconfig == "" {
		return fmt.Errorf("copying within the same namespace requires --to-name to avoid name collision")
	}

	// Validate on-conflict
	switch o.OnConflict {
	case "skip", "warn", "overwrite":
	default:
		return fmt.Errorf("invalid --on-conflict value %q: must be skip, warn, or overwrite", o.OnConflict)
	}

	// Validate output
	switch o.Output {
	case "table", "yaml", "json":
	default:
		return fmt.Errorf("invalid --output value %q: must be table, yaml, or json", o.Output)
	}

	return nil
}

// TargetName returns the target resource name, falling back to the source name.
func (o *Options) TargetName() string {
	if o.ToName != "" {
		return o.ToName
	}
	return o.ResourceName
}

// Run executes the copy operation.
func (o *Options) Run() error {
	ctx := context.TODO()

	// Build clients
	clients, err := client.New(o.SourceKubeconfig, o.SourceContext, o.ToKubeconfig, o.ToContext)
	if err != nil {
		return fmt.Errorf("initializing clients: %w", err)
	}

	gvr := ResolveGVR(o.ResourceKind)

	primaryRef := copier.ResourceRef{
		GVR:       gvr,
		Name:      o.ResourceName,
		Namespace: o.SourceNamespace,
	}

	// Build list of resources to copy
	refs := []copier.ResourceRef{primaryRef}

	if o.Recursive {
		discovered, err := discovery.Discover(ctx, clients.SourceDynamic, primaryRef.GVR, primaryRef.Name, primaryRef.Namespace)
		if err != nil {
			return fmt.Errorf("discovering dependencies: %w", err)
		}
		refs = append(refs, discovered...)
	}

	// Execute copy
	c := &copier.Copier{
		SourceClient: clients.SourceDynamic,
		TargetClient: clients.TargetDynamic,
		OnConflict:   o.OnConflict,
		DryRun:       o.DryRun,
	}

	results := c.CopyAll(ctx, refs, o.ToNamespace, o.ToName)

	// Output results
	return output.Print(results, o.Output, o.DryRun)
}

// ResolveGVR maps a user-provided resource kind string to a GroupVersionResource
// using common aliases. For less common types, it falls back to assuming the string
// is already a resource name in the core group.
func ResolveGVR(kind string) schema.GroupVersionResource {
	aliases := map[string]schema.GroupVersionResource{
		"deployment":            {Group: "apps", Version: "v1", Resource: "deployments"},
		"deployments":           {Group: "apps", Version: "v1", Resource: "deployments"},
		"deploy":                {Group: "apps", Version: "v1", Resource: "deployments"},
		"statefulset":           {Group: "apps", Version: "v1", Resource: "statefulsets"},
		"statefulsets":          {Group: "apps", Version: "v1", Resource: "statefulsets"},
		"sts":                   {Group: "apps", Version: "v1", Resource: "statefulsets"},
		"daemonset":             {Group: "apps", Version: "v1", Resource: "daemonsets"},
		"daemonsets":            {Group: "apps", Version: "v1", Resource: "daemonsets"},
		"ds":                    {Group: "apps", Version: "v1", Resource: "daemonsets"},
		"replicaset":            {Group: "apps", Version: "v1", Resource: "replicasets"},
		"replicasets":           {Group: "apps", Version: "v1", Resource: "replicasets"},
		"rs":                    {Group: "apps", Version: "v1", Resource: "replicasets"},
		"pod":                   {Group: "", Version: "v1", Resource: "pods"},
		"pods":                  {Group: "", Version: "v1", Resource: "pods"},
		"po":                    {Group: "", Version: "v1", Resource: "pods"},
		"service":               {Group: "", Version: "v1", Resource: "services"},
		"services":              {Group: "", Version: "v1", Resource: "services"},
		"svc":                   {Group: "", Version: "v1", Resource: "services"},
		"configmap":             {Group: "", Version: "v1", Resource: "configmaps"},
		"configmaps":            {Group: "", Version: "v1", Resource: "configmaps"},
		"cm":                    {Group: "", Version: "v1", Resource: "configmaps"},
		"secret":                {Group: "", Version: "v1", Resource: "secrets"},
		"secrets":               {Group: "", Version: "v1", Resource: "secrets"},
		"serviceaccount":        {Group: "", Version: "v1", Resource: "serviceaccounts"},
		"serviceaccounts":       {Group: "", Version: "v1", Resource: "serviceaccounts"},
		"sa":                    {Group: "", Version: "v1", Resource: "serviceaccounts"},
		"persistentvolumeclaim": {Group: "", Version: "v1", Resource: "persistentvolumeclaims"},
		"persistentvolumeclaims": {Group: "", Version: "v1", Resource: "persistentvolumeclaims"},
		"pvc":                    {Group: "", Version: "v1", Resource: "persistentvolumeclaims"},
		"ingress":                {Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
		"ingresses":              {Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
		"ing":                    {Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
		"job":                    {Group: "batch", Version: "v1", Resource: "jobs"},
		"jobs":                   {Group: "batch", Version: "v1", Resource: "jobs"},
		"cronjob":                {Group: "batch", Version: "v1", Resource: "cronjobs"},
		"cronjobs":               {Group: "batch", Version: "v1", Resource: "cronjobs"},
		"cj":                     {Group: "batch", Version: "v1", Resource: "cronjobs"},
		"horizontalpodautoscaler":  {Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"},
		"horizontalpodautoscalers": {Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"},
		"hpa":                      {Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"},
		"networkpolicy":            {Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
		"networkpolicies":          {Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
		"netpol":                   {Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
	}

	if gvr, ok := aliases[kind]; ok {
		return gvr
	}

	// Fallback: assume core group resource
	return schema.GroupVersionResource{Group: "", Version: "v1", Resource: kind}
}

// getDefaultNamespace returns the namespace from the current kubeconfig context.
func getDefaultNamespace(kubeconfig, context string) string {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		rules.ExplicitPath = kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if context != "" {
		overrides.CurrentContext = context
	}
	cfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
	ns, _, err := cfg.Namespace()
	if err != nil || ns == "" {
		return "default"
	}
	return ns
}
