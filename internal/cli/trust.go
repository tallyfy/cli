package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tallyfy/cli/internal/config"
)

func init() {
	register(func(root *cobra.Command) {
		cmd := &cobra.Command{
			Use:   "trust",
			Short: "Trust this workspace so its project hooks may run",
			Long: `Mark the current workspace as trusted.

Project- and local-scope hooks (` + "`.tallyfy/settings.json`" + `) run only in a
trusted workspace. This prevents a cloned repository from executing commands
on your machine just because you ran a tallyfy command inside it.

  tallyfy trust           # trust the current workspace
  tallyfy trust status    # is the current workspace trusted?
  tallyfy trust remove    # revoke trust`,
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				ctx, dir, err := trustContext(cmd)
				if err != nil {
					return err
				}
				if err := config.TrustWorkspace(dir); err != nil {
					return err
				}
				ctx.Infof("trusted workspace %s\n", dir)
				return nil
			},
		}
		cmd.AddCommand(trustStatusCmd(), trustRemoveCmd())
		root.AddCommand(cmd)
	})
}

func trustStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report whether the current workspace is trusted",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, dir, err := trustContext(cmd)
			if err != nil {
				return err
			}
			trusted := config.WorkspaceTrusted(dir)
			status := "untrusted"
			if trusted {
				status = "trusted"
			}
			return ctx.RenderItem([]string{"WORKSPACE", "STATUS"}, []string{dir, status},
				map[string]any{"workspace": dir, "trusted": trusted})
		},
	}
}

func trustRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove",
		Aliases: []string{"revoke", "untrust"},
		Short:   "Revoke trust for the current workspace",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, dir, err := trustContext(cmd)
			if err != nil {
				return err
			}
			if err := config.UntrustWorkspace(dir); err != nil {
				return err
			}
			ctx.Infof("revoked trust for workspace %s\n", dir)
			return nil
		},
	}
}

// trustContext resolves the workspace directory: the discovered .tallyfy
// project dir, or the current working directory when none was found.
func trustContext(cmd *cobra.Command) (*Context, string, error) {
	ctx, err := NewContext(cmd, false)
	if err != nil {
		return nil, "", err
	}
	dir := ctx.Cfg.ProjectDir
	if dir == "" {
		dir, err = os.Getwd()
		if err != nil {
			return nil, "", err
		}
	}
	return ctx, dir, nil
}
