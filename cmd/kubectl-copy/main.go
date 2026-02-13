package main

import (
	"fmt"
	"os"

	copycmd "github.com/a13x22/kubecopy/pkg/cmd"
)

// version is set at build time via -ldflags.
var version = "dev"

func main() {
	cmd := copycmd.NewCopyCommand()
	cmd.Version = version

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
