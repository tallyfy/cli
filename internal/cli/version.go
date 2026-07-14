package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/tallyfy/cli/internal/output"
	"github.com/tallyfy/cli/internal/version"
)

func init() {
	register(func(root *cobra.Command) {
		cmd := &cobra.Command{
			Use:   "version",
			Short: "Print version, commit, and build date",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				jsonOut, _ := cmd.Flags().GetBool("json")
				mode, _ := cmd.Flags().GetString("output")
				if jsonOut || mode == "json" {
					return output.Render(cmd.OutOrStdout(), output.ModeJSON, output.Table{
						JSONItems: []any{map[string]string{
							"version": version.Version,
							"commit":  version.Commit,
							"date":    version.Date,
							"os":      runtime.GOOS + "/" + runtime.GOARCH,
						}},
					})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "tallyfy %s (commit %s, built %s, %s/%s)\n",
					version.Version, version.Commit, version.Date, runtime.GOOS, runtime.GOARCH)
				return nil
			},
		}
		root.AddCommand(cmd)
	})
}
