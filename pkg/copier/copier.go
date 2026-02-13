package copier

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/a13x22/kubecopy/pkg/conflict"
	"github.com/a13x22/kubecopy/pkg/sanitizer"
)

// ResourceRef uniquely identifies a Kubernetes resource to be copied.
type ResourceRef struct {
	GVR       schema.GroupVersionResource
	Name      string
	Namespace string
}

// CopyResult records what happened with a single resource copy operation.
type CopyResult struct {
	Source      ResourceRef
	TargetName  string
	TargetNS    string
	Action      string // "created", "skipped", "overwritten", "dry-run"
	Warnings    []sanitizer.Warning
	Conflicts   []conflict.Conflict
	Error       error
	Sanitized   *unstructured.Unstructured // the sanitized object (for dry-run output)
}

// Copier performs the fetch-sanitize-detect-create pipeline.
type Copier struct {
	SourceClient dynamic.Interface
	TargetClient dynamic.Interface
	OnConflict   string // "skip", "warn", "overwrite"
	DryRun       bool
}

// Copy fetches a single resource from the source, sanitizes it, checks for
// conflicts, and creates it in the target.
func (c *Copier) Copy(ctx context.Context, ref ResourceRef, targetNS, targetName string) CopyResult {
	result := CopyResult{
		Source:     ref,
		TargetName: targetName,
		TargetNS:   targetNS,
	}

	if targetName == "" {
		targetName = ref.Name
		result.TargetName = targetName
	}

	// 1. Fetch from source
	obj, err := c.SourceClient.Resource(ref.GVR).Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		result.Error = fmt.Errorf("fetch %s/%s in %s: %w", ref.GVR.Resource, ref.Name, ref.Namespace, err)
		return result
	}

	// 2. Deep copy and sanitize
	copied := obj.DeepCopy()
	warnings := sanitizer.Run(copied, targetNS, targetName)
	result.Warnings = warnings
	result.Sanitized = copied

	// 3. Conflict detection
	conflicts := conflict.Detect(ctx, c.TargetClient, ref.GVR, copied, targetNS)
	result.Conflicts = conflicts

	// If there are hard conflicts, handle based on strategy
	if hasExistence := conflictHasType(conflicts, conflict.TypeExistence); hasExistence {
		switch c.OnConflict {
		case "skip":
			result.Action = "skipped"
			return result
		case "warn":
			// Continue, but action indicates it was overwritten with a warning
			result.Action = "overwritten"
		case "overwrite":
			result.Action = "overwritten"
		}
	}

	// 4. Dry-run: do not create
	if c.DryRun {
		if result.Action == "" {
			result.Action = "dry-run"
		} else {
			result.Action = result.Action + " (dry-run)"
		}
		return result
	}

	// 5. Create or update in target
	if result.Action == "overwritten" {
		// Delete then recreate (to handle immutable field changes)
		_ = c.TargetClient.Resource(ref.GVR).Namespace(targetNS).Delete(ctx, targetName, metav1.DeleteOptions{})
		_, err = c.TargetClient.Resource(ref.GVR).Namespace(targetNS).Create(ctx, copied, metav1.CreateOptions{})
	} else {
		_, err = c.TargetClient.Resource(ref.GVR).Namespace(targetNS).Create(ctx, copied, metav1.CreateOptions{})
	}

	if err != nil {
		result.Error = fmt.Errorf("create %s/%s in %s: %w", ref.GVR.Resource, targetName, targetNS, err)
		return result
	}

	if result.Action == "" {
		result.Action = "created"
	}
	return result
}

// CopyAll copies a list of resources, typically built by the dependency graph walker.
// The first item in the list is the primary resource (may have a custom target name);
// subsequent items keep their original names.
func (c *Copier) CopyAll(ctx context.Context, refs []ResourceRef, targetNS, primaryTargetName string) []CopyResult {
	var results []CopyResult
	for i, ref := range refs {
		name := ref.Name
		if i == 0 && primaryTargetName != "" {
			name = primaryTargetName
		}
		result := c.Copy(ctx, ref, targetNS, name)
		results = append(results, result)
	}
	return results
}

func conflictHasType(conflicts []conflict.Conflict, t conflict.Type) bool {
	for _, c := range conflicts {
		if c.Type == t {
			return true
		}
	}
	return false
}
