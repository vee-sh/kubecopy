package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"sigs.k8s.io/yaml"

	"github.com/a13x22/kube-copy/pkg/copier"
)

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
	colorBold   = "\033[1m"
)

// PrintPlan shows the planned actions before execution (or for --dry-run).
func PrintPlan(results []copier.CopyResult, format string) error {
	switch format {
	case "yaml":
		return printYAML(results, os.Stdout)
	case "json":
		return printJSON(results, os.Stdout)
	default:
		return printPlanTable(results, os.Stderr)
	}
}

// PrintResults shows what actually happened after apply.
func PrintResults(results []copier.CopyResult, format string) error {
	switch format {
	case "yaml":
		return printYAML(results, os.Stdout)
	case "json":
		return printJSON(results, os.Stdout)
	default:
		return printResultsTable(results, os.Stderr)
	}
}

func printPlanTable(results []copier.CopyResult, w io.Writer) error {
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "  %s%sACTION\tRESOURCE\tSOURCE\tTARGET%s\n", colorBold, colorGray, colorReset)
	fmt.Fprintf(tw, "  %s------\t--------\t------\t------%s\n", colorGray, colorReset)

	for _, r := range results {
		if r.Error != nil {
			fmt.Fprintf(tw, "  %serror\t%s\t%s/%s\t%s/%s%s\n",
				colorRed,
				r.Source.DisplayName(),
				r.Source.Namespace, r.Source.Name,
				r.TargetNS, r.TargetName,
				colorReset)
			continue
		}

		color, symbol := actionStyle(r.Action)
		fmt.Fprintf(tw, "  %s%s %s\t%s\t%s/%s\t%s/%s%s\n",
			color, symbol, r.Action,
			r.Source.DisplayName(),
			r.Source.Namespace, r.Source.Name,
			r.TargetNS, r.TargetName,
			colorReset)
	}
	tw.Flush()

	// Warnings and conflicts
	printWarningsAndConflicts(results, w)

	// Errors detail
	for _, r := range results {
		if r.Error != nil {
			fmt.Fprintf(w, "\n  %sERROR %s:%s %v\n", colorRed, r.Source.DisplayName(), colorReset, r.Error)
		}
	}

	// Summary line
	printPlanSummary(results, w)

	return nil
}

func printResultsTable(results []copier.CopyResult, w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)

	for _, r := range results {
		if r.Error != nil {
			fmt.Fprintf(tw, "  %sx  %s\t-> %s/%s\t%s%s\n",
				colorRed,
				r.Source.DisplayName(),
				r.TargetNS, r.TargetName,
				"ERROR",
				colorReset)
			continue
		}

		color, symbol := doneStyle(r.Action)
		fmt.Fprintf(tw, "  %s%s  %-12s\t%s -> %s/%s%s\n",
			color, symbol, r.Action,
			r.Source.DisplayName(),
			r.TargetNS, r.TargetName,
			colorReset)
	}
	tw.Flush()

	// Errors detail
	for _, r := range results {
		if r.Error != nil {
			fmt.Fprintf(w, "  %sERROR %s:%s %v\n", colorRed, r.Source.DisplayName(), colorReset, r.Error)
		}
	}

	// Summary
	printDoneSummary(results, w)

	return nil
}

func actionStyle(action string) (string, string) {
	switch action {
	case "create":
		return colorGreen, "+"
	case "skip":
		return colorYellow, "-"
	case "overwrite":
		return colorYellow, "~"
	default:
		return colorCyan, "?"
	}
}

func doneStyle(action string) (string, string) {
	switch action {
	case "created":
		return colorGreen, "+"
	case "skipped":
		return colorYellow, "-"
	case "overwritten":
		return colorYellow, "~"
	default:
		return colorRed, "x"
	}
}

func printWarningsAndConflicts(results []copier.CopyResult, w io.Writer) {
	hasAny := false
	for _, r := range results {
		if len(r.Warnings) > 0 || len(r.Conflicts) > 0 {
			hasAny = true
			break
		}
	}
	if !hasAny {
		return
	}

	fmt.Fprintln(w)
	for _, r := range results {
		for _, warn := range r.Warnings {
			fmt.Fprintf(w, "  %sWARN%s  %s: %s\n", colorYellow, colorReset, warn.Resource, warn.Message)
		}
		for _, c := range r.Conflicts {
			fmt.Fprintf(w, "  %sCONFLICT [%s]%s %s\n", colorRed, c.Type, colorReset, c.Message)
		}
	}
}

func printPlanSummary(results []copier.CopyResult, w io.Writer) {
	creates := countAction(results, "create")
	skips := countAction(results, "skip")
	overwrites := countAction(results, "overwrite")
	errors := countErrors(results)

	fmt.Fprintf(w, "\n  %sPlan: %d resource(s)", colorGray, len(results))
	if creates > 0 {
		fmt.Fprintf(w, ", %s%d to create%s", colorGreen, creates, colorGray)
	}
	if skips > 0 {
		fmt.Fprintf(w, ", %s%d to skip%s", colorYellow, skips, colorGray)
	}
	if overwrites > 0 {
		fmt.Fprintf(w, ", %s%d to overwrite%s", colorYellow, overwrites, colorGray)
	}
	if errors > 0 {
		fmt.Fprintf(w, ", %s%d error(s)%s", colorRed, errors, colorGray)
	}
	fmt.Fprintf(w, "%s\n", colorReset)
}

func printDoneSummary(results []copier.CopyResult, w io.Writer) {
	created := countAction(results, "created")
	skipped := countAction(results, "skipped")
	overwritten := countAction(results, "overwritten")
	errors := countErrors(results)

	fmt.Fprintf(w, "\n  %sDone: %d resource(s)", colorGray, len(results))
	if created > 0 {
		fmt.Fprintf(w, ", %s%d created%s", colorGreen, created, colorGray)
	}
	if skipped > 0 {
		fmt.Fprintf(w, ", %s%d skipped%s", colorYellow, skipped, colorGray)
	}
	if overwritten > 0 {
		fmt.Fprintf(w, ", %s%d overwritten%s", colorYellow, overwritten, colorGray)
	}
	if errors > 0 {
		fmt.Fprintf(w, ", %s%d error(s)%s", colorRed, errors, colorGray)
	}
	fmt.Fprintf(w, "%s\n\n", colorReset)
}

func countAction(results []copier.CopyResult, action string) int {
	count := 0
	for _, r := range results {
		if r.Action == action {
			count++
		}
	}
	return count
}

func countErrors(results []copier.CopyResult) int {
	count := 0
	for _, r := range results {
		if r.Error != nil {
			count++
		}
	}
	return count
}

// ---- YAML / JSON output (for piping) ----

func printYAML(results []copier.CopyResult, w io.Writer) error {
	objects := collectObjects(results)

	if len(objects) == 1 {
		data, err := yaml.Marshal(objects[0])
		if err != nil {
			return err
		}
		fmt.Fprint(w, string(data))
		return nil
	}

	list := buildList(objects)
	data, err := yaml.Marshal(list)
	if err != nil {
		return err
	}
	fmt.Fprint(w, string(data))
	return nil
}

func printJSON(results []copier.CopyResult, w io.Writer) error {
	objects := collectObjects(results)

	if len(objects) == 1 {
		data, err := json.MarshalIndent(objects[0], "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(w, string(data))
		return nil
	}

	list := buildList(objects)
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(data))
	return nil
}

func collectObjects(results []copier.CopyResult) []map[string]interface{} {
	var objects []map[string]interface{}
	for _, r := range results {
		if r.Sanitized != nil {
			objects = append(objects, r.Sanitized.Object)
		}
	}
	return objects
}

func buildList(objects []map[string]interface{}) map[string]interface{} {
	items := make([]interface{}, len(objects))
	for i, o := range objects {
		items[i] = o
	}
	return map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      items,
	}
}

// FormatResourceRef returns a human-readable string for a resource.
func FormatResourceRef(kind, name string) string {
	return fmt.Sprintf("%s/%s", kind, name)
}

// Print is a backwards-compatible wrapper. Deprecated: use PrintPlan/PrintResults.
func Print(results []copier.CopyResult, format string, dryRun bool) error {
	if dryRun {
		return PrintPlan(results, format)
	}
	return PrintResults(results, format)
}

