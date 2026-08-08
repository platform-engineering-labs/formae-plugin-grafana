// Command gen writes the Pkl module declaring a contact point's typed settings
// from the embedded notifier metadata snapshot. Run it via `make generate`.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/platform-engineering-labs/formae-plugin-grafana/internal/notifiers"
)

func main() {
	out := flag.String("o", notifiers.GeneratedSettingsPath, "path of the Pkl module to write")
	flag.Parse()

	if err := run(*out); err != nil {
		fmt.Fprintf(os.Stderr, "gen: %v\n", err)
		os.Exit(1)
	}
}

func run(out string) error {
	source, err := notifiers.RenderContactPointSettings(notifiers.Baked())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(out), err)
	}
	if err := os.WriteFile(out, []byte(source), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", out, err)
	}
	return nil
}
