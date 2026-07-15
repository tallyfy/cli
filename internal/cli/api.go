package cli

import (
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tallyfy/cli/internal/hooks"
)

func init() {
	register(func(root *cobra.Command) {
		cmd := &cobra.Command{
			Use:   "api <METHOD> <path>",
			Short: "Make an authenticated raw API request",
			Long: `Call any Tallyfy API endpoint directly, with the standard headers,
authentication, and retry policy applied.

  tallyfy api GET  organizations/{org}/checklists
  tallyfy api GET  me
  tallyfy api POST organizations/{org}/tags --input tag.json
  tallyfy api GET  organizations/{org}/runs --query status=active --query per_page=100

The path is relative to the API base URL (no leading slash needed). The
response body is written to stdout verbatim; non-2xx responses still print
the body and map to the usual exit codes (5 not-found, 7 validation, ...).`,
			Args: cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx, err := NewContext(cmd, true)
				if err != nil {
					return err
				}
				method := strings.ToUpper(args[0])
				path := strings.TrimPrefix(args[1], "/")

				queryPairs, _ := cmd.Flags().GetStringArray("query")
				query := url.Values{}
				for _, p := range queryPairs {
					k, v, ok := strings.Cut(p, "=")
					if !ok || k == "" {
						return &UsageError{Msg: "invalid --query " + p + " (want key=value)"}
					}
					query.Add(k, v)
				}

				var body []byte
				if input, _ := cmd.Flags().GetString("input"); input != "" {
					body, err = readInput(input)
					if err != nil {
						return &UsageError{Msg: err.Error()}
					}
				}

				if err := ctx.Guard("Api", "request", "", hooks.Payload{Resource: "api", ID: method + " " + path}); err != nil {
					return err
				}
				if ctx.DryRun {
					ctx.DryRunf("%s %s query=%v body=%dB", method, path, query, len(body))
					return nil
				}

				_, respBody, apiErr := ctx.API.Raw(cmd.Context(), method, path, query, body)
				// Print the body regardless of status so scripts can inspect errors.
				if len(respBody) > 0 {
					if werr := l6WritePrettyJSON(respBody, ""); werr != nil {
						return werr
					}
				}
				return apiErr
			},
		}
		cmd.Flags().StringArray("query", nil, "query parameter as key=value (repeatable)")
		cmd.Flags().String("input", "", "request body: a file path, or \"-\" for stdin")
		root.AddCommand(cmd)
	})
}
