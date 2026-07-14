// Command tallyfy is the official Tallyfy command-line interface.
//
// It provides deterministic, scriptable access to Tallyfy workflows:
// launching processes, completing tasks, exporting and importing blueprints,
// and gating CI/CD pipelines on human approvals.
package main

import (
	"os"

	"github.com/tallyfy/cli/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
