package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/a13x22/kubecopy/pkg/copier"
)

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
)

// Print renders copy results in the requested format.
func Print(results []copier.CopyResult, format string, dryRun bool) error {
	switch format {
	case "yaml":
		return printYAML(results, os.Stdout)
	case "json":
		return printJSON(results, os.Stdout)
	default:
		return printTable(results, os.Stdout, dryRun)
	}
}

func printTable(results []copier.CopyResult, w io.Writer, dryRun bool) error {
	if dryRun {
		fmt.Fprintf(w, "\n%s--- DRY RUN (no changes made) ---%s\n\n", colorCyan, colorReset)
	}

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "  %sSTATUS\tRESOURCE\tSOURCE\tTARGET%s\n", colorGray, colorReset)
	fmt.Fprintf(tw, "  %s------\t--------\t------\t------%s\n", colorGray, colorReset)

	var errCount int

	for _, r := range results {
		if r.Error != nil {
			errCount++
			fmt.Fprintf(tw, "  %sERROR\t%s/%s\t%s/%s\t%s/%s%s\n",
				colorRed,
				r.Source.GVR.Resource, r.Source.Name,
				r.Source.Namespace, r.Source.Name,
				r.TargetNS, r.TargetName,
				colorReset)
			fmt.Fprintf(tw, "  %s  -> %v%s\n", colorRed, r.Error, colorReset)
			continue
		}

		color := colorGreen
		symbol := "+"
		switch {
		case r.Action == "skipped":
			color = colorYellow
			symbol = "-"
		case strings.HasPrefix(r.Action, "overwritten"):
			color = colorYellow
			symbol = "~"
		case strings.HasPrefix(r.Action, "dry-run"):
			color = colorCyan
			symbol = "?"
		}

		fmt.Fprintf(tw, "  %s%s %s\t%s/%s\t%s/%s\t%s/%s%s\n",
			color, symbol, r.Action,
			r.Source.GVR.Resource, r.Source.Name,
			r.Source.Namespace, r.Source.Name,
			r.TargetNS, r.TargetName,
			colorReset)
	}

	tw.Flush()

	// Print warnings and conflicts below the table
	for _, r := range results {
		for _, warn := range r.Warnings {
			fmt.Fprintf(w, "  %sWARNING%s %s: %s\n", colorYellow, colorReset, warn.Resource, warn.Message)
		}
		for _, c := range r.Conflicts {
			fmt.Fprintf(w, "  %sCONFLICT [%s]%s %s\n", colorRed, c.Type, colorReset, c.Message)
		}
	}

	// Summary
	total := len(results)
	created := countAction(results, "created")
	skipped := countAction(results, "skipped")
	overwritten := countAction(results, "overwritten")
	dryRunCount := countAction(results, "dry-run")

	fmt.Fprintf(w, "\n  %sSummary: %d resource(s) processed", colorGray, total)
	if created > 0 {
		fmt.Fprintf(w, ", %s%d created%s", colorGreen, created, colorGray)
	}
	if skipped > 0 {
		fmt.Fprintf(w, ", %s%d skipped%s", colorYellow, skipped, colorGray)
	}
	if overwritten > 0 {
		fmt.Fprintf(w, ", %s%d overwritten%s", colorYellow, overwritten, colorGray)
	}
	if dryRunCount > 0 {
		fmt.Fprintf(w, ", %s%d would be created%s", colorCyan, dryRunCount, colorGray)
	}
	if errCount > 0 {
		fmt.Fprintf(w, ", %s%d error(s)%s", colorRed, errCount, colorGray)
	}
	fmt.Fprintf(w, "%s\n\n", colorReset)

	return nil
}

func printYAML(results []copier.CopyResult, w io.Writer) error {
	var objects []map[string]interface{}
	for _, r := range results {
		if r.Sanitized != nil {
			objects = append(objects, r.Sanitized.Object)
		}
	}

	if len(objects) == 1 {
		data, err := yaml.Marshal(objects[0])
		if err != nil {
			return err
		}
		fmt.Fprint(w, string(data))
		return nil
	}

	// Multi-document YAML
	list := buildList(results)
	data, err := yaml.Marshal(list)
	if err != nil {
		return err
	}
	fmt.Fprint(w, string(data))
	return nil
}

func printJSON(results []copier.CopyResult, w io.Writer) error {
	if len(results) == 1 && results[0].Sanitized != nil {
		data, err := json.MarshalIndent(results[0].Sanitized.Object, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(w, string(data))
		return nil
	}

	list := buildList(results)
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(data))
	return nil
}

func buildList(results []copier.CopyResult) map[string]interface{} {
	var items []interface{}
	for _, r := range results {
		if r.Sanitized != nil {
			items = append(items, r.Sanitized.Object)
		}
	}
	return map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      items,
	}
}

func countAction(results []copier.CopyResult, prefix string) int {
	count := 0
	for _, r := range results {
		if strings.HasPrefix(r.Action, prefix) {
			count++
		}
	}
	return count
}

// FormatResourceRef returns a human-readable string for a resource.
func FormatResourceRef(obj *unstructured.Unstructured) string {
	return fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName())
}
