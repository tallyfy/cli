// Package saved implements saved commands (spec §6.9): team-standardized
// operations defined as YAML files in .tallyfy/commands/<name>.yaml (project)
// and ~/.tallyfy/commands/<name>.yaml (user; project overrides user), invoked
// via `tallyfy run <name> --param value`.
//
// THIS FILE IS THE FROZEN CONTRACT consumed by internal/cli.
package saved

// Command is one saved command definition.
type Command struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	// Command is the tallyfy subcommand line with {{param}} placeholders,
	// e.g.: process launch --blueprint "Onboarding" --field name={{name}}
	Command string   `yaml:"command"`
	Params  []string `yaml:"params"`
	// Source is stamped by the loader: "project" or "user".
	Source string `yaml:"-"`
	// Path is the file it was loaded from.
	Path string `yaml:"-"`
}
