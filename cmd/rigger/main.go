// Command rigger sets a machine up from a setup repo: it shows what will be installed and
// linked, lets you pick, installs, links everything back to the repo and keeps machines in sync.
package main

import (
	"fmt"
	"os"
)

// version is set by goreleaser.
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "rigger:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 && (args[0] == "version" || args[0] == "--version") {
		fmt.Println("rigger", version)
		return nil
	}
	return fmt.Errorf("unknown command %q", args)
}
