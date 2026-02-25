package copier

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/a13x22/kube-copy/pkg/conflict"
	"github.com/a13x22/kube-copy/pkg/sanitizer"
)

// ResourceRef uniquely identifies a Kubernetes resource to be copied.
type ResourceRef struct {
	GVR        schema.GroupVersionResource
	Kind       string // human-friendly kind, e.g. "Deployment"
	Name       string
	Namespace  string
	Namespaced bool // false for cluster-scoped (StorageClass, Node, ClusterRole, etc.)
}

// DisplayName returns "Kind/Name" for human-friendly display.
func (r ResourceRef) DisplayName() string {
	if r.Kind != "" {
		return r.Kind + "/" + r.Name
	}
	return r.GVR.Resource + "/" + r.Name
}

// CopyResult records what happened with a single resource copy operation.
type CopyResult struct {
	Source    ResourceRef
	TargetName string
	TargetNS   string
	Action     string // "create", "skip", "overwrite" (plan); "created", "skipped", "overwritten" (done)
	Warnings   []sanitizer.Warning
	Conflicts  []conflict.Conflict
	Error      error
	Sanitized  *unstructured.Unstructured // the sanitized object
}

// Progress reports real-time status during copy operations.
type Progress interface {
	Connecting()
	Fetching(displayName, namespace string)
	Sanitizing(displayName string)
	Checking(displayName string)
	Creating(displayName, namespace string)
	Discovered(count int)
}

// noopProgress is used when no progress reporter is set.
type noopProgress struct{}

func (noopProgress) Connecting()                         {}
func (noopProgress) Fetching(string, string)             {}
func (noopProgress) Sanitizing(string)                   {}
func (noopProgress) Checking(string)                     {}
func (noopProgress) Creating(string, string)             {}
func (noopProgress) Discovered(int)                      {}

// Copier performs the fetch-sanitize-detect-create pipeline.
type Copier struct {
	SourceClient dynamic.Interface
	TargetClient dynamic.Interface
	OnConflict   string // "skip", "warn", "overwrite"
	Progress     Progress
}

func (c *Copier) progress() Progress {
	if c.Progress != nil {
		return c.Progress
	}
	return noopProgress{}
}

// Plan fetches a single resource, sanitizes it, checks for conflicts,
// but does NOT create it. Returns the planned result.
func (c *Copier) Plan(ctx context.Context, ref ResourceRef, targetNS, targetName string) CopyResult {
	result := CopyResult{
		Source:     ref,
		TargetName: targetName,
		TargetNS:   targetNS,
	}

	if targetName == "" {
		targetName = ref.Name
		result.TargetName = targetName
	}

	p := c.progress()

	// 1. Fetch from source (use empty namespace for cluster-scoped resources)
	srcNS := ref.Namespace
	if !ref.Namespaced {
		srcNS = ""
	}
	p.Fetching(ref.DisplayName(), ref.Namespace)
	obj, err := c.SourceClient.Resource(ref.GVR).Namespace(srcNS).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		result.Error = FormatFetchError(err, ref)
		return result
	}

	// 2. Deep copy and sanitize
	p.Sanitizing(ref.DisplayName())
	copied := obj.DeepCopy()
	warnings := sanitizer.Run(copied, targetNS, targetName)
	result.Warnings = warnings
	result.Sanitized = copied

	// 3. Conflict detection
	p.Checking(ref.DisplayName())
	conflicts := conflict.Detect(ctx, c.TargetClient, ref.GVR, copied, targetNS)
	result.Conflicts = conflicts

	// Determine planned action
	if conflictHasType(conflicts, conflict.TypeExistence) {
		switch c.OnConflict {
		case "skip":
			result.Action = "skip"
		case "warn", "overwrite":
			result.Action = "overwrite"
		}
	} else {
		result.Action = "create"
	}

	return result
}

// Apply executes a planned result -- creates the resource in the target cluster.
// Only call this after Plan. Skipped resources are left alone.
func (c *Copier) Apply(ctx context.Context, planned *CopyResult) {
	if planned.Error != nil || planned.Action == "skip" {
		if planned.Action == "skip" {
			planned.Action = "skipped"
		}
		return
	}

	ref := planned.Source
	targetNS := planned.TargetNS
	targetName := planned.TargetName
	copied := planned.Sanitized
	if !ref.Namespaced {
		targetNS = ""
	}

	p := c.progress()
	p.Creating(ref.DisplayName(), targetNS)

	var err error
	if planned.Action == "overwrite" {
		_ = c.TargetClient.Resource(ref.GVR).Namespace(targetNS).Delete(ctx, targetName, metav1.DeleteOptions{})
		_, err = c.TargetClient.Resource(ref.GVR).Namespace(targetNS).Create(ctx, copied, metav1.CreateOptions{})
		planned.Action = "overwritten"
	} else {
		_, err = c.TargetClient.Resource(ref.GVR).Namespace(targetNS).Create(ctx, copied, metav1.CreateOptions{})
		planned.Action = "created"
	}

	if err != nil {
		planned.Error = FormatCreateError(err, ref, targetNS)
	}
}

// PlanAll plans all resources in the list without creating anything.
func (c *Copier) PlanAll(ctx context.Context, refs []ResourceRef, targetNS, primaryTargetName string) []CopyResult {
	var results []CopyResult
	for i, ref := range refs {
		name := ref.Name
		if i == 0 && primaryTargetName != "" {
			name = primaryTargetName
		}
		result := c.Plan(ctx, ref, targetNS, name)
		results = append(results, result)
	}
	return results
}

// ApplyAll executes all planned results.
func (c *Copier) ApplyAll(ctx context.Context, planned []CopyResult) {
	for i := range planned {
		c.Apply(ctx, &planned[i])
	}
}

func conflictHasType(conflicts []conflict.Conflict, t conflict.Type) bool {
	for _, c := range conflicts {
		if c.Type == t {
			return true
		}
	}
	return false
}

// FormatFetchError wraps a fetch error with a human-friendly message.
func FormatFetchError(err error, ref ResourceRef) error {
	raw := err.Error()
	switch {
	case contains(raw, "the server could not find the requested resource"):
		return fmt.Errorf("%s: resource type not recognized by the cluster API server.\n"+
			"    Verify the resource exists: kubectl api-resources | grep %s",
			ref.DisplayName(), ref.GVR.Resource)
	case contains(raw, "not found"):
		return fmt.Errorf("%s not found in namespace %q.\n"+
			"    Run: kubectl get %s -n %s",
			ref.DisplayName(), ref.Namespace, ref.GVR.Resource, ref.Namespace)
	case contains(raw, "Unauthorized") || contains(raw, "forbidden"):
		return fmt.Errorf("%s: permission denied in namespace %q.\n"+
			"    Check your RBAC roles and kubeconfig context.",
			ref.DisplayName(), ref.Namespace)
	case contains(raw, "dial tcp") || contains(raw, "connection refused") || contains(raw, "no such host"):
		return fmt.Errorf("cannot reach cluster: %w\n"+
			"    Check your kubeconfig context and network connectivity.", err)
	default:
		return fmt.Errorf("fetch %s in %s: %w", ref.DisplayName(), ref.Namespace, err)
	}
}

// FormatCreateError wraps a create error with a human-friendly message.
func FormatCreateError(err error, ref ResourceRef, targetNS string) error {
	raw := err.Error()
	switch {
	case contains(raw, "already exists"):
		return fmt.Errorf("%s already exists in namespace %q.\n"+
			"    Use --on-conflict=overwrite to replace it.",
			ref.DisplayName(), targetNS)
	case contains(raw, "Unauthorized") || contains(raw, "forbidden"):
		return fmt.Errorf("%s: permission denied creating in namespace %q.\n"+
			"    Check your RBAC roles for the target cluster/namespace.",
			ref.DisplayName(), targetNS)
	default:
		return fmt.Errorf("create %s in %s: %w", ref.DisplayName(), targetNS, err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
