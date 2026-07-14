// Package mcpconfig manages .tallyfy/mcp.json (project-scoped MCP server
// registrations) and generates ready-to-paste client config snippets for
// Claude Code, Claude Desktop, and Cursor (spec §6.6).
//
// Note: the hosted Tallyfy MCP server (https://mcp.tallyfy.com) uses its own
// OAuth 2.1 flow; the CLI does NOT share its personal-token credential with
// it. `tallyfy mcp` manages configuration wiring only.
//
// THIS FILE IS THE FROZEN CONTRACT consumed by internal/cli.
package mcpconfig

// Server is one MCP server entry in .tallyfy/mcp.json.
type Server struct {
	Type string   `json:"type"`              // "http" | "stdio"
	URL  string   `json:"url,omitempty"`     // http servers
	Cmd  string   `json:"command,omitempty"` // stdio servers
	Args []string `json:"args,omitempty"`
}

// File is the .tallyfy/mcp.json document shape.
type File struct {
	MCPServers map[string]Server `json:"mcpServers"`
}

// DefaultTallyfyServer is the hosted Tallyfy MCP server entry installed by
// `tallyfy mcp add tallyfy`.
var DefaultTallyfyServer = Server{Type: "http", URL: "https://mcp.tallyfy.com"}
