package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tallyfy/cli/internal/update"
	"github.com/tallyfy/cli/internal/version"
)

func init() {
	register(func(root *cobra.Command) {
		cmd := &cobra.Command{
			Use:   "update",
			Short: "Update tallyfy to the latest release",
			Long: `Check for and install the latest tallyfy release.

  tallyfy update           # update to the latest release on your channel
  tallyfy update --check   # report whether an update is available, install nothing

Downloaded binaries are verified against the release checksums before the
running executable is atomically replaced. Homebrew-managed installs are
directed to ` + "`brew upgrade`" + ` instead.`,
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				ctx, err := NewContext(cmd, false)
				if err != nil {
					return err
				}
				channel := update.Channel(ctx.Cfg.UpdateChannel)
				if flag, _ := cmd.Flags().GetString("channel"); flag != "" {
					channel = update.Channel(flag)
				}

				res, err := update.Check(cmd.Context(), version.Version, channel)
				if err != nil {
					return err
				}

				if !res.UpdateAvailable {
					ctx.Infof("tallyfy %s is already the latest on the %s channel\n", res.Current, res.Channel)
					return ctx.RenderItem([]string{"CURRENT", "LATEST", "CHANNEL", "UPDATE"},
						[]string{res.Current, res.Latest, res.Channel, "false"}, res)
				}

				checkOnly, _ := cmd.Flags().GetBool("check")
				if checkOnly {
					ctx.Infof("update available: %s -> %s (run `tallyfy update`)\n", res.Current, res.Latest)
					return ctx.RenderItem([]string{"CURRENT", "LATEST", "CHANNEL", "UPDATE"},
						[]string{res.Current, res.Latest, res.Channel, "true"}, res)
				}

				ctx.Infof("updating tallyfy %s -> %s...\n", res.Current, res.Latest)
				if err := update.Apply(cmd.Context(), res, os.Stderr); err != nil {
					return err
				}
				ctx.Infof("updated to %s\n", res.Latest)
				return nil
			},
		}
		cmd.Flags().Bool("check", false, "only check for an update; do not install")
		cmd.Flags().String("channel", "", "release channel: stable or latest (default from settings)")
		root.AddCommand(cmd)
	})
}
