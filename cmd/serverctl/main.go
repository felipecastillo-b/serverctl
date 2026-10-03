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
		fmt.Fprintf(flag.CommandLine.Output(),
			"serverctl — local-first TUI for Linux server administration over SSH\n\nUsage: serverctl [flags]\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	fmt.Printf("serverctl %s — TUI arrives in milestone M1 (see ROADMAP.md)\n", version)
}
