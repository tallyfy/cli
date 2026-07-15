package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/tallyfy/cli/internal/auth"
)

func init() {
	register(func(root *cobra.Command) {
		cmd := &cobra.Command{
			Use:   "auth",
			Short: "Manage authentication (login, logout, status)",
			Long: `Manage how the CLI authenticates to Tallyfy.

` + "`tallyfy login`" + ` and ` + "`tallyfy logout`" + ` are aliases for
` + "`tallyfy auth login`" + ` and ` + "`tallyfy auth logout`" + `.

Credentials resolve in this order: --api-key flag, TALLYFY_API_TOKEN, the
auth.apiKeyHelper script, then the token saved by ` + "`tallyfy login`" + `.`,
		}
		cmd.AddCommand(authLoginCmd(), authLogoutCmd(), authStatusCmd())
		root.AddCommand(cmd)

		// Top-level conveniences: `tallyfy login` / `tallyfy logout`.
		login := authLoginCmd()
		login.Use = "login"
		root.AddCommand(login)
		logout := authLogoutCmd()
		logout.Use = "logout"
		root.AddCommand(logout)
	})
}

func authLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Save an API token to the OS keychain",
		Long: `Authenticate by pasting a personal API token.

  tallyfy login

Opens your Tallyfy settings in a browser, then prompts you to paste a token.
The token is validated against GET /me and stored in the OS keychain (or an
AES-256-GCM encrypted file when no keychain is available).

Non-interactive:
  echo "$TOKEN" | tallyfy login --stdin`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := NewContext(cmd, false)
			if err != nil {
				return err
			}
			baseURL := ctx.Cfg.BaseURL
			fromStdin, _ := cmd.Flags().GetBool("stdin")
			noBrowser, _ := cmd.Flags().GetBool("no-browser")

			var token string
			switch {
			case fromStdin || !ctx.Interactive:
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				token = strings.TrimSpace(line)
			default:
				loginURL := auth.LoginURL(baseURL)
				ctx.Infof("Open this URL to create a token, then paste it below:\n  %s\n", loginURL)
				if !noBrowser {
					_ = auth.OpenBrowser(loginURL)
				}
				fmt.Fprint(os.Stderr, "Paste your API token: ")
				b, rerr := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Fprintln(os.Stderr)
				if rerr != nil {
					return fmt.Errorf("read token: %w", rerr)
				}
				token = strings.TrimSpace(string(b))
			}
			if token == "" {
				return &UsageError{Msg: "no token provided"}
			}

			me, err := auth.ValidateToken(baseURL, token)
			if err != nil {
				return err
			}
			store := auth.NewStore()
			if err := store.Save(token); err != nil {
				return err
			}
			ctx.Infof("Logged in as %s (%s). Token saved to the %s.\n",
				me.Email, me.ID.String(), store.Backend())
			return nil
		},
	}
	cmd.Flags().Bool("stdin", false, "read the token from stdin instead of prompting")
	cmd.Flags().Bool("no-browser", false, "do not open a browser (just print the URL)")
	return cmd
}

func authLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Delete the saved API token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := NewContext(cmd, false)
			if err != nil {
				return err
			}
			store := auth.NewStore()
			if err := store.Delete(); err != nil {
				return err
			}
			ctx.Infof("Logged out. Saved token deleted from the %s.\n", store.Backend())
			return nil
		},
	}
}

func authStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the active credential source and identity",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := NewContext(cmd, false)
			if err != nil {
				return err
			}
			apiKey, _ := cmd.Flags().GetString("api-key")
			cred, resolveErr := auth.NewResolver().Resolve(ctx.Cfg, apiKey)
			store := auth.NewStore()

			source := "none"
			identity := ""
			if resolveErr == nil && cred != nil {
				source = string(cred.Source)
				if me, verr := auth.ValidateToken(ctx.Cfg.BaseURL, cred.Token); verr == nil {
					identity = fmt.Sprintf("%s (%s)", me.Email, me.ID.String())
				} else {
					identity = "token present but validation failed: " + verr.Error()
				}
			}
			cols := []string{"SOURCE", "BACKEND", "IDENTITY"}
			row := []string{source, store.Backend(), identity}
			return ctx.RenderItem(cols, row, map[string]string{
				"source":   source,
				"backend":  store.Backend(),
				"identity": identity,
			})
		},
	}
}
