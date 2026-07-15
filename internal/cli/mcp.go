package cli

import (
	"github.com/spf13/cobra"

	"github.com/tallyfy/cli/internal/mcpconfig"
)

func init() {
	register(func(root *cobra.Command) {
		cmd := &cobra.Command{
			Use:   "mcp",
			Short: "Manage MCP server entries in .tallyfy/mcp.json",
			Long: `Manage Model Context Protocol (MCP) server definitions for this project.

These entries live in .tallyfy/mcp.json and let AI clients discover the
Tallyfy MCP server (and any others you add). Use ` + "`mcp snippet`" + ` to print
the equivalent config block for a specific AI client.`,
		}
		cmd.AddCommand(mcpListCmd(), mcpAddCmd(), mcpRemoveCmd(), mcpSnippetCmd())
		root.AddCommand(cmd)
	})
}

func mcpListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured MCP servers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := NewContext(cmd, false)
			if err != nil {
				return err
			}
			f, err := mcpconfig.Load(ctx.Cfg.ProjectDir)
			if err != nil {
				return err
			}
			cols := []string{"NAME", "TYPE", "URL/COMMAND"}
			rows := make([][]string, 0, len(f.MCPServers))
			items := make([]any, 0, len(f.MCPServers))
			for name, s := range f.MCPServers {
				target := s.URL
				if target == "" {
					target = s.Cmd
				}
				rows = append(rows, []string{name, s.Type, target})
				items = append(items, map[string]any{"name": name, "server": s})
			}
			ctx.Infof("config file: %s\n", mcpconfig.Path(ctx.Cfg.ProjectDir))
			return ctx.RenderList(cols, rows, items)
		},
	}
}

func mcpAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add or update an MCP server",
		Long: `Add an MCP server entry.

  tallyfy mcp add tallyfy                         # the hosted Tallyfy MCP server
  tallyfy mcp add myhttp --url https://mcp.example.com
  tallyfy mcp add mytool --command my-mcp --arg --port --arg 8080`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := NewContext(cmd, false)
			if err != nil {
				return err
			}
			name := args[0]
			urlFlag, _ := cmd.Flags().GetString("url")
			cmdFlag, _ := cmd.Flags().GetString("command")
			argFlags, _ := cmd.Flags().GetStringArray("arg")

			var server mcpconfig.Server
			switch {
			case name == "tallyfy" && urlFlag == "" && cmdFlag == "":
				server = mcpconfig.DefaultTallyfyServer
			case urlFlag != "":
				server = mcpconfig.Server{Type: "http", URL: urlFlag}
			case cmdFlag != "":
				server = mcpconfig.Server{Type: "stdio", Cmd: cmdFlag, Args: argFlags}
			default:
				return &UsageError{Msg: "provide --url (http server) or --command (stdio server)"}
			}

			f, err := mcpconfig.Load(ctx.Cfg.ProjectDir)
			if err != nil {
				return err
			}
			mcpconfig.Add(f, name, server)
			if err := mcpconfig.Save(ctx.Cfg.ProjectDir, f); err != nil {
				return err
			}
			ctx.Infof("added MCP server %q to %s\n", name, mcpconfig.Path(ctx.Cfg.ProjectDir))
			return nil
		},
	}
	cmd.Flags().String("url", "", "HTTP MCP server URL")
	cmd.Flags().String("command", "", "stdio MCP server command")
	cmd.Flags().StringArray("arg", nil, "argument for a stdio server command (repeatable)")
	return cmd
}

func mcpRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an MCP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := NewContext(cmd, false)
			if err != nil {
				return err
			}
			name := args[0]
			f, err := mcpconfig.Load(ctx.Cfg.ProjectDir)
			if err != nil {
				return err
			}
			if !mcpconfig.Remove(f, name) {
				return &UsageError{Msg: "no MCP server named " + name}
			}
			if err := mcpconfig.Save(ctx.Cfg.ProjectDir, f); err != nil {
				return err
			}
			ctx.Infof("removed MCP server %q\n", name)
			return nil
		},
	}
}

func mcpSnippetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snippet <name>",
		Short: "Print an AI client's config block for a configured server",
		Long: `Print the configuration block to paste into an AI client so it can reach
one of your configured MCP servers.

  tallyfy mcp snippet tallyfy --client claude`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := NewContext(cmd, false)
			if err != nil {
				return err
			}
			name := args[0]
			client, _ := cmd.Flags().GetString("client")
			f, err := mcpconfig.Load(ctx.Cfg.ProjectDir)
			if err != nil {
				return err
			}
			server, ok := f.MCPServers[name]
			if !ok {
				return &UsageError{Msg: "no MCP server named " + name + " (run `tallyfy mcp list`)"}
			}
			snippet, err := mcpconfig.Snippet(client, name, server)
			if err != nil {
				return &UsageError{Msg: err.Error()}
			}
			ctx.Printf("%s\n", snippet)
			return nil
		},
	}
	cmd.Flags().String("client", "claude", "target AI client (e.g. claude)")
	return cmd
}
