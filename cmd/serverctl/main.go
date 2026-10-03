// Command serverctl is a local-first, single-binary TUI for Linux server
// administration and monitoring over SSH.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/felipecastillo-b/serverctl/internal/config"
	"github.com/felipecastillo-b/serverctl/internal/ui/app"
	"github.com/felipecastillo-b/serverctl/internal/ui/keys"
)

// version is injected at build time via -ldflags "-X main.version=<version>".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	configPath := flag.String("config", config.Path(), "path to the YAML config file")
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

	if *showVersion {
		fmt.Printf("serverctl %s\n", version)
		return
	}

	if err := run(*configPath); err != nil {
		// Best-effort report: if stderr itself fails, the non-zero exit
		// code is the only signal left.
		_, _ = fmt.Fprintf(os.Stderr, "serverctl: %v\n", err)
		os.Exit(1)
	}
}

// run loads the configuration, validates the key overrides before entering
// the alternate screen, and starts the TUI. A missing config file is not
// an error: config.Load falls back to the defaults.
func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	// app.New applies the overrides trusting they are valid, so they must
	// be validated here first to fail fast with a readable error.
	global := keys.DefaultGlobal()
	if err := global.ApplyOverrides(cfg.Keys); err != nil {
		return fmt.Errorf("invalid keybinding override: %w", err)
	}

	program := tea.NewProgram(app.New(cfg), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("run TUI: %w", err)
	}
	return nil
}
