package cli

import (
	"github.com/spf13/cobra"

	"github.com/tallyfy/cli/internal/config"
)

func init() {
	register(func(root *cobra.Command) {
		cmd := &cobra.Command{
			Use:     "config",
			Aliases: []string{"settings"},
			Short:   "Read and write configuration settings",
			Long: `Read and write the CLI's layered configuration.

Settings merge across six scopes (defaults < user < project < local < flags
< managed). Scalars override; list keys (permissions, hooks, MCP servers)
merge. Use ` + "`config list --show-sources`" + ` to see which file set each value.`,
		}
		cmd.AddCommand(configGetCmd(), configSetCmd(), configListCmd())
		root.AddCommand(cmd)
	})
}

func configGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Print the resolved value of one setting",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := NewContext(cmd, false)
			if err != nil {
				return err
			}
			key := args[0]
			val, ok := config.Get(ctx.Cfg, key)
			if !ok {
				return &UsageError{Msg: "unknown setting " + key + " (run `tallyfy config list`)"}
			}
			ctx.Printf("%s\n", val)
			return nil
		},
	}
}

func configSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Write a setting to a scope's settings file",
		Long: `Write a setting into a scope's settings file.

  tallyfy config set output json                 # user scope (default)
  tallyfy config set org org_123 --scope project # .tallyfy/settings.json
  tallyfy config set telemetry.enabled false

Managed settings (/etc/tallyfy, %PROGRAMDATA%\Tallyfy) are read-only here.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := NewContext(cmd, false)
			if err != nil {
				return err
			}
			scope, _ := cmd.Flags().GetString("scope")
			if err := config.SetKey(scope, ctx.Cfg.ProjectDir, args[0], args[1]); err != nil {
				return &UsageError{Msg: err.Error()}
			}
			ctx.Infof("set %s = %s (%s scope)\n", args[0], args[1], scope)
			return nil
		},
	}
	cmd.Flags().String("scope", "user", "which settings file to write: user, project, or local")
	return cmd
}

func configListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List effective settings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := NewContext(cmd, false)
			if err != nil {
				return err
			}
			withSources, _ := cmd.Flags().GetBool("show-sources")
			entries := config.ListEffective(ctx.Cfg)

			if withSources {
				cols := []string{"KEY", "VALUE", "SCOPE", "FILE"}
				rows := make([][]string, 0, len(entries))
				items := make([]any, 0, len(entries))
				for _, e := range entries {
					rows = append(rows, []string{e.Key, e.Value, e.Scope.String(), e.File})
					items = append(items, map[string]string{
						"key": e.Key, "value": e.Value, "scope": e.Scope.String(), "file": e.File,
					})
				}
				return ctx.RenderList(cols, rows, items)
			}
			cols := []string{"KEY", "VALUE"}
			rows := make([][]string, 0, len(entries))
			items := make([]any, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{e.Key, e.Value})
				items = append(items, map[string]string{"key": e.Key, "value": e.Value})
			}
			return ctx.RenderList(cols, rows, items)
		},
	}
	cmd.Flags().Bool("show-sources", false, "show the scope and file that set each value")
	return cmd
}
