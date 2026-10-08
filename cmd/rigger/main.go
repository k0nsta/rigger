// Command rigger sets a machine up from a setup repo: it shows what will be installed and
// linked, lets you pick, installs, links everything back to the repo and keeps machines in sync.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/k0nsta/rigger/internal/manifest"
)

// version is set by goreleaser.
var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "rigger:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: rigger <version|validate [dir]>")
	}
	switch args[0] {
	case "version", "--version":
		_, err := fmt.Fprintln(out, "rigger", version)
		return err
	case "validate":
		dir := "."
		if len(args) > 1 {
			dir = args[1]
		}
		m, err := manifest.Load(dir)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%s: %d components, %d profiles, ok\n", manifest.FileName, len(m.Components), len(m.Profiles))
		return err
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
