// Package tallyfy is a lean, typed Go client for the Tallyfy REST API
// (https://api.tallyfy.com). It bakes in the platform's sharp edges so
// callers never re-implement them:
//
//   - the mandatory X-Tallyfy-Client header on every request
//   - Authorization: Bearer and Accept: application/json headers
//   - base URL resolution
//   - pagination iteration for list endpoints
//   - 429/5xx retry with Retry-After support, exponential backoff and jitter
//   - typed error mapping (auth / not-found / validation / rate-limited)
//
// Response envelope (League/Fractal DataArraySerializer):
//
//	item:      {"data": {...}}
//	list:      {"data": [...], "meta": {"pagination": {...}}}
//	error:     {"error": true, "message": "..."} (also {"message": ...} variants)
//
// THIS FILE IS THE FROZEN CONTRACT consumed by internal/cli. Implementation
// lives in the sibling files of this package.
package tallyfy

import (
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// DefaultBaseURL is the public production API host (root paths, no /api prefix).
const DefaultBaseURL = "https://api.tallyfy.com"

// ClientHeader is the mandatory X-Tallyfy-Client value for public API clients.
const ClientHeader = "APIClient"

// Options configures a Client.
type Options struct {
	BaseURL    string // default DefaultBaseURL
	Token      string // bearer token; required for all org endpoints
	HTTPClient *http.Client
	UserAgent  string // e.g. "tallyfy-cli/0.1.0 (darwin/arm64)"
	MaxRetries int    // retry budget for 429/5xx; default 4
	// Verbose, when non-nil, receives one line per request/response with the
	// Authorization header redacted.
	Verbose io.Writer
}

// Client is a Tallyfy REST API client. Safe for concurrent use.
type Client struct {
	opts Options
	hc   *http.Client
}

// Pagination mirrors meta.pagination in list responses.
type Pagination struct {
	Total       int             `json:"total"`
	Count       int             `json:"count"`
	PerPage     int             `json:"per_page"`
	CurrentPage int             `json:"current_page"`
	TotalPages  int             `json:"total_pages"`
	Links       PaginationLinks `json:"links"`
}

// PaginationLinks holds the previous/next page URLs when present.
// The API emits links either as an object or an empty array; a custom
// unmarshaler in the implementation tolerates both.
type PaginationLinks struct {
	Previous string
	Next     string
}

// Meta is the non-data half of a list envelope.
type Meta struct {
	Pagination *Pagination `json:"pagination,omitempty"`
}

// Page is one page of raw list results plus its pagination metadata.
type Page struct {
	Data json.RawMessage
	Meta *Meta
}

// ListOptions control pagination of list requests.
type ListOptions struct {
	Page    int // 1-based; 0 = first page
	PerPage int // server caps at 100; 0 = server default
	// All, when true, makes Paginate follow every page.
	All bool
	// Limit caps the total number of items fetched across pages (0 = no cap).
	Limit int
	// With adds ?with=a,b relationship includes.
	With []string
	// Extra query params merged into the request.
	Extra map[string]string
}

// --- Core resource models -------------------------------------------------
//
// Field sets are intentionally lean: the columns the CLI renders plus the
// identifiers automation needs. Raw JSON is always available via -o json,
// which re-serializes the API payload untouched. Shapes verified against the
// live Swagger 2.0 spec (temporary/tallyfy-cli-build/swagger/) and the
// api-support reference scripts.

// Me is the current authenticated user (GET /me).
type Me struct {
	ID        json.Number `json:"id"`
	Email     string      `json:"email"`
	Username  string      `json:"username"`
	FirstName string      `json:"first_name"`
	LastName  string      `json:"last_name"`
	Timezone  string      `json:"timezone"`
}

// Organization is one org the user belongs to (GET /me/organizations).
type Organization struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

// Blueprint (API name: checklist) is a workflow template.
type Blueprint struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Summary     string          `json:"summary"`
	Status      string          `json:"status"`
	Owner       json.Number     `json:"owner_id"`
	StepsCount  json.Number     `json:"steps_count"`
	CreatedAt   string          `json:"created_at"`
	LastUpdated string          `json:"last_updated"`
	ArchivedAt  *string         `json:"archived_at"`
	Steps       json.RawMessage `json:"steps,omitempty"`
	Raw         json.RawMessage `json:"-"` // full payload for export round-trips
}

// KickoffOption is one choice on a radio/dropdown/multiselect kick-off field,
// or one column on a table field. The API emits option ids as numbers, so the
// id is kept as raw JSON and echoed back in exactly the form it arrived in.
type KickoffOption struct {
	ID    json.RawMessage `json:"id"`
	Text  string          `json:"text"`
	Label string          `json:"label"` // table columns carry label, not text
}

// KickoffField is one kick-off form field ("prerun") on a blueprint.
//
// ID is the field's timeline_id, and it is THE key a launch body's "prerun"
// object must use: api-v2 resolves each supplied key with
// allKickOffFields()->firstWhere('timeline_id', $key) and skips anything it
// cannot find, so a key that is not a timeline_id is dropped silently
// (App\Http\Requests\Runs\RunRequestValidator::validatePrerun).
type KickoffField struct {
	ID        string          `json:"id"`
	Alias     string          `json:"alias"`
	Label     string          `json:"label"`
	FieldType string          `json:"field_type"`
	Options   []KickoffOption `json:"options"`
	Columns   []KickoffOption `json:"columns"`
}

// Process (API name: run) is a launched instance of a blueprint.
type Process struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Status       string          `json:"status"`
	ChecklistID  string          `json:"checklist_id"`
	ProgressJSON json.RawMessage `json:"progress,omitempty"`
	StartedBy    json.Number     `json:"started_by"`
	CreatedAt    string          `json:"created_at"`
	LastUpdated  string          `json:"last_updated"`
	ArchivedAt   *string         `json:"archived_at"`
	DueDate      *string         `json:"due_date"`
}

// Task is a unit of work inside a process (or a standalone one-off task).
type Task struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Status      string          `json:"status"`
	RunID       *string         `json:"run_id"`
	StepID      *string         `json:"step_id"`
	Deadline    *string         `json:"deadline"`
	CompletedAt *string         `json:"completed_at"`
	Owners      json.RawMessage `json:"owners,omitempty"`
	TaskType    string          `json:"task_type"`
	CreatedAt   string          `json:"created_at"`
}

// User is an organization member.
type User struct {
	ID        json.Number `json:"id"`
	Email     string      `json:"email"`
	FirstName string      `json:"first_name"`
	LastName  string      `json:"last_name"`
	Role      string      `json:"role"`
	Status    string      `json:"status"`
	LastLogin *string     `json:"last_login_at"`
}

// Guest is an external participant identified by email.
type Guest struct {
	Email     string          `json:"email"`
	FirstName string          `json:"first_name"`
	LastName  string          `json:"last_name"`
	Disabled  json.RawMessage `json:"disabled_at,omitempty"`
	CreatedAt string          `json:"created_at"`
}

// Group is a named set of members/guests used for assignment.
type Group struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Members   json.RawMessage `json:"members,omitempty"`
	Guests    json.RawMessage `json:"guests,omitempty"`
	CreatedAt string          `json:"created_at"`
}

// Folder organizes blueprints/processes.
type Folder struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	ParentID  *string `json:"parent_id"`
	CreatedAt string  `json:"created_at"`
}

// Tag labels blueprints and processes.
type Tag struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Color string `json:"color"`
}

// Comment is a task comment/thread entry.
type Comment struct {
	ID        string      `json:"id"`
	Content   string      `json:"content"`
	AuthorID  json.Number `json:"author_id"`
	CreatedAt string      `json:"created_at"`
}

// RetryConfig captures backoff behavior (exposed for tests).
type RetryConfig struct {
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
}
