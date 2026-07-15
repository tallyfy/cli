package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/tallyfy/cli/internal/saved"
)

func init() {
	register(func(root *cobra.Command) {
		cmd := &cobra.Command{
			Use:   "run [name] [--param key=value ...]",
			Short: "Run a saved command from .tallyfy/commands",
			Long: `Run a reusable command saved in .tallyfy/commands/*.yaml (or the user-level
~/.tallyfy/commands).

  tallyfy run                       # list saved commands
  tallyfy run onboard --param name="Jo Lee" --param manager=jo@example.com

Each saved command names a tallyfy subcommand line with {{param}} placeholders;
--param values fill them in before the command runs in-process.`,
			Args: cobra.ArbitraryArgs,
			// DisableFlagParsing keeps --param and target flags out of this
			// command's own flag set; we parse --param manually and hand the rest
			// to the resolved command line.
			DisableFlagParsing: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx, err := NewContext(cmd, false)
				if err != nil {
					return err
				}
				cmds, err := saved.LoadAll(ctx.Cfg.ProjectDir)
				if err != nil {
					return err
				}

				name, params, passthrough, err := parseRunArgs(args)
				if err != nil {
					return err
				}
				if name == "" {
					return renderSavedList(ctx, cmds)
				}

				sc, ok := saved.Find(cmds, name)
				if !ok {
					return &UsageError{Msg: "no saved command named " + name + " (run `tallyfy run` to list)"}
				}
				expanded, err := saved.Expand(*sc, params)
				if err != nil {
					return &UsageError{Msg: err.Error()}
				}
				expanded = append(expanded, passthrough...)

				// Execute the resolved command line against a fresh command tree so
				// global flags and PersistentPreRun wiring apply normally.
				fresh := NewRootCmd()
				fresh.SetArgs(expanded)
				return fresh.Execute()
			},
		}
		root.AddCommand(cmd)
	})
}

// parseRunArgs splits `run` arguments into the saved-command name, its
// --param key=value map, and any remaining passthrough arguments.
func parseRunArgs(args []string) (name string, params map[string]string, passthrough []string, err error) {
	params = map[string]string{}
	i := 0
	// First non-flag token is the command name.
	for i < len(args) {
		if !strings.HasPrefix(args[i], "-") {
			name = args[i]
			i++
			break
		}
		// A leading flag before the name is unexpected for `run`.
		return "", nil, nil, &UsageError{Msg: "usage: tallyfy run <name> [--param key=value ...]"}
	}
	for i < len(args) {
		switch {
		case args[i] == "--param" || args[i] == "-p":
			if i+1 >= len(args) {
				return "", nil, nil, &UsageError{Msg: "--param needs a key=value argument"}
			}
			k, v, ok := strings.Cut(args[i+1], "=")
			if !ok || k == "" {
				return "", nil, nil, &UsageError{Msg: "invalid --param " + args[i+1] + " (want key=value)"}
			}
			params[k] = v
			i += 2
		case strings.HasPrefix(args[i], "--param="):
			kv := strings.TrimPrefix(args[i], "--param=")
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k == "" {
				return "", nil, nil, &UsageError{Msg: "invalid --param " + kv + " (want key=value)"}
			}
			params[k] = v
			i++
		default:
			passthrough = append(passthrough, args[i])
			i++
		}
	}
	return name, params, passthrough, nil
}

func renderSavedList(ctx *Context, cmds []saved.Command) error {
	cols := []string{"NAME", "DESCRIPTION", "PARAMS", "SOURCE"}
	rows := make([][]string, 0, len(cmds))
	items := make([]any, 0, len(cmds))
	for _, c := range cmds {
		rows = append(rows, []string{c.Name, truncate(c.Description, 50), strings.Join(c.Params, ","), c.Source})
		items = append(items, c)
	}
	if len(cmds) == 0 {
		ctx.Infof("no saved commands found (add YAML files under .tallyfy/commands/)\n")
	}
	return ctx.RenderList(cols, rows, items)
}
