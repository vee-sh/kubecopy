package output

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

// ProgressReporter writes real-time status updates to stderr.
// Uses carriage return to overwrite lines for a clean look.
// Automatically disables itself when stderr is not a terminal or quiet mode is on.
type ProgressReporter struct {
	enabled   bool
	lastLen   int
}

// NewProgress creates a new progress reporter.
// Disabled when quiet=true or stderr is not a terminal.
func NewProgress(quiet bool) *ProgressReporter {
	enabled := !quiet && term.IsTerminal(int(os.Stderr.Fd()))
	return &ProgressReporter{enabled: enabled}
}

func (p *ProgressReporter) write(msg string) {
	if !p.enabled {
		return
	}
	// Clear previous line
	if p.lastLen > 0 {
		fmt.Fprintf(os.Stderr, "\r%*s\r", p.lastLen, "")
	}
	fmt.Fprintf(os.Stderr, "  %s%s%s", colorGray, msg, colorReset)
	p.lastLen = len(msg) + 2 // +2 for "  " prefix
}

// Clear removes the progress line.
func (p *ProgressReporter) Clear() {
	if !p.enabled || p.lastLen == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "\r%*s\r", p.lastLen, "")
	p.lastLen = 0
}

// Connecting reports that the tool is connecting to the cluster.
func (p *ProgressReporter) Connecting() {
	p.write("Connecting to cluster...")
}

// Fetching reports that a resource is being fetched.
func (p *ProgressReporter) Fetching(displayName, namespace string) {
	p.write(fmt.Sprintf("Fetching %s from %s...", displayName, namespace))
}

// Sanitizing reports that a resource is being sanitized.
func (p *ProgressReporter) Sanitizing(displayName string) {
	p.write(fmt.Sprintf("Sanitizing %s...", displayName))
}

// Checking reports that conflicts are being checked.
func (p *ProgressReporter) Checking(displayName string) {
	p.write(fmt.Sprintf("Checking conflicts for %s...", displayName))
}

// Creating reports that a resource is being created.
func (p *ProgressReporter) Creating(displayName, namespace string) {
	p.write(fmt.Sprintf("Creating %s in %s...", displayName, namespace))
}

// Discovering reports that dependency discovery is in progress.
func (p *ProgressReporter) Discovering() {
	p.write("Discovering dependencies...")
}

// DiscoveredCount reports how many dependencies were found.
func (p *ProgressReporter) DiscoveredCount(count int) {
	if count == 0 {
		p.write("No additional dependencies found.")
	} else {
		p.write(fmt.Sprintf("Found %d related resource(s).", count))
	}
}

// Discovered implements copier.Progress interface.
func (p *ProgressReporter) Discovered(count int) {
	p.DiscoveredCount(count)
}
