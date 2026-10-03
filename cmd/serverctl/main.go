// Command serverctl is a local-first, single-binary TUI for Linux server
// administration and monitoring over SSH.
package main

import (
	"flag"
	"fmt"
)

// version is injected at build time via -ldflags "-X main.version=<version>".
var version = "dev"

func main() {
	flag.Usage = func() {
		// Best-effort usage print: there is no useful recovery if writing
		// to stderr fails, but errcheck still requires an explicit decision.
		if _, err := fmt.Fprintf(flag.CommandLine.Output(),
			"serverctl — local-first TUI for Linux server administration over SSH\n\nUsage: serverctl [flags]\n"); err != nil {
			return
		}
		flag.PrintDefaults()
	}
	flag.Parse()

	fmt.Printf("serverctl %s — TUI arrives in milestone M1 (see ROADMAP.md)\n", version)
}
