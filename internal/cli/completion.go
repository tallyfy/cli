package cli

import (
	"os"

	"github.com/spf13/cobra"
)

func init() {
	register(func(root *cobra.Command) {
		cmd := &cobra.Command{
			Use:       "completion [bash|zsh|fish|powershell]",
			Short:     "Generate a shell completion script",
			Long: `Generate a shell completion script for tallyfy.

Bash:
  source <(tallyfy completion bash)
  # persistent: tallyfy completion bash > /usr/local/etc/bash_completion.d/tallyfy

Zsh:
  tallyfy completion zsh > "${fpath[1]}/_tallyfy"

Fish:
  tallyfy completion fish > ~/.config/fish/completions/tallyfy.fish

PowerShell:
  tallyfy completion powershell | Out-String | Invoke-Expression`,
			Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
			ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
			// Completion scripts never need auth or config.
			RunE: func(cmd *cobra.Command, args []string) error {
				root := cmd.Root()
				switch args[0] {
				case "bash":
					return root.GenBashCompletionV2(os.Stdout, true)
				case "zsh":
					return root.GenZshCompletion(os.Stdout)
				case "fish":
					return root.GenFishCompletion(os.Stdout, true)
				case "powershell":
					return root.GenPowerShellCompletionWithDesc(os.Stdout)
				}
				return &UsageError{Msg: "unknown shell " + args[0]}
			},
		}
		root.AddCommand(cmd)
	})
}
