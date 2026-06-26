package models

import (
	"regexp"
	"time"
)

// EnvVarRefPattern matches ${KEY} or ${KEY:-default} patterns in env var values.
var EnvVarRefPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// ProjectEnvVarRefPattern matches $[project:environment:var] patterns.
// Example: $[myapp:dev:GITHUB_TOKEN] resolves to the GITHUB_TOKEN value
// stored under project "myapp", environment "dev".
var ProjectEnvVarRefPattern = regexp.MustCompile(`\$\[([A-Za-z0-9_.-]+):([A-Za-z0-9_.-]+):([A-Za-z_][A-Za-z0-9_]*)\]`)

// HasEnvVarRef returns true if the value contains any env var reference pattern.
func HasEnvVarRef(val string) bool {
	return EnvVarRefPattern.MatchString(val) || ProjectEnvVarRefPattern.MatchString(val)
}

// Server represents a backend MCP server that the proxy connects to.
type Server struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Transport      string            `json:"transport"` // "stdio", "http", or "streamable-http"
	Command        string            `json:"command,omitempty"`
	Args           []string          `json:"args,omitempty"`
	URL            string            `json:"url,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	AuthToken      string            `json:"-"`               // never serialized — write-only via API
	HasAuthToken   bool              `json:"has_auth_token"`  // indicates a token is set (never exposes the value)
	AuthMethod     string            `json:"auth_method"`              // "none", "oauth", "bearer", "env_bearer"
	BearerTokenEnv string            `json:"bearer_token_env,omitempty"` // env var name for env_bearer method
	Timeout        int               `json:"timeout"`
	ConnectTimeout int               `json:"connect_timeout"`
	Enabled        bool              `json:"enabled"`
	LogsEnabled    bool              `json:"logs_enabled"` // capture stderr logs for this server
	IsBuiltin      bool              `json:"is_builtin"`   // builtin servers can't be edited/deleted
	BuiltinType    string            `json:"builtin_type,omitempty"` // "memory", "skills", "tasks"
	Labels         []string          `json:"labels,omitempty"`        // user-facing labels for categorization
	Tags           []string          `json:"tags,omitempty"`         // system tags for internal identification
	Status         string            `json:"status"` // "connected", "disconnected", "error"`
	LastSeen       *time.Time        `json:"last_seen,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

// BuiltinMemoryServerID is the virtual server ID for the built-in memory MCP server.
const BuiltinMemoryServerID = "builtin-memory"

// BuiltinSkillServerID is the virtual server ID for the built-in skill MCP server.
const BuiltinSkillServerID = "builtin-skills"

// BuiltinServer returns a virtual Server record for the built-in memory server.
func BuiltinMemoryServer() Server {
	return Server{
		ID:        BuiltinMemoryServerID,
		Name:      "memory",
		Transport: "builtin",
		Enabled:   true,
		IsBuiltin: true,
		Status:    "connected",
	}
}

// APIKey represents an authentication key for accessing the proxy.
type APIKey struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	KeyHash     string     `json:"-"`
	KeyPrefix   string     `json:"key_prefix"`
	Scopes      []string   `json:"scopes"` // "read", "write", "admin"
	CompoundID  *string    `json:"compound_id,omitempty"` // if set, key only exposes this compound's tools
	Active      bool       `json:"active"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// User represents an admin user for the web UI.
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"` // "admin"
	CreatedAt    time.Time `json:"created_at"`
}

// CompoundServer represents a named group of MCP servers.
type CompoundServer struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Description    string    `json:"description,omitempty"`
	DictionaryMode bool      `json:"dictionary_mode"` // when true, expose a single dictionary tool instead of all member tools
	CreatedAt      time.Time `json:"created_at"`
}

// CompoundServerWithMembers includes the member server IDs.
type CompoundServerWithMembers struct {
	CompoundServer
	Members         []Server `json:"members"`
	ToolCount       int       `json:"tool_count"`         // total tools (server + memory)
	ServerToolCount int       `json:"server_tool_count"`  // tools from real MCP servers
	MemoryToolCount int       `json:"memory_tool_count"`  // tools from built-in memory sets
}

// Tool represents a discovered MCP tool from a backend server.
type Tool struct {
	ServerID   string                 `json:"server_id"`
	ServerName string                 `json:"server_name"`
	Name       string                 `json:"name"`
	Description string                `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema,omitempty"`
}

// CreateServerRequest is the payload for creating a new server.
type CreateServerRequest struct {
	Name           string              `json:"name"`
	Transport      string              `json:"transport"`
	Command        string              `json:"command,omitempty"`
	Args           []string            `json:"args,omitempty"`
	URL            string              `json:"url,omitempty"`
	Headers        map[string]string   `json:"headers,omitempty"`
	Env            map[string]string   `json:"env,omitempty"`
	AuthToken      string              `json:"auth_token,omitempty"`
	AuthMethod     string              `json:"auth_method,omitempty"`
	BearerTokenEnv string              `json:"bearer_token_env,omitempty"`
	Timeout        int                 `json:"timeout,omitempty"`
	ConnectTimeout int                 `json:"connect_timeout,omitempty"`
	Enabled        *bool               `json:"enabled,omitempty"`
	LogsEnabled    *bool               `json:"logs_enabled,omitempty"`
	Labels         []string            `json:"labels,omitempty"`
	Tags           []string            `json:"tags,omitempty"`
}

// UpdateServerRequest is the payload for updating a server.
type UpdateServerRequest struct {
	Name           *string             `json:"name,omitempty"`
	Transport      *string             `json:"transport,omitempty"`
	Command        *string             `json:"command,omitempty"`
	Args           *[]string           `json:"args,omitempty"`
	URL            *string             `json:"url,omitempty"`
	Headers        *map[string]string  `json:"headers,omitempty"`
	Env            *map[string]string  `json:"env,omitempty"`
	AuthToken      *string             `json:"auth_token,omitempty"`
	AuthMethod     *string             `json:"auth_method,omitempty"`
	BearerTokenEnv *string             `json:"bearer_token_env,omitempty"`
	Timeout        *int                `json:"timeout,omitempty"`
	ConnectTimeout *int                `json:"connect_timeout,omitempty"`
	Enabled        *bool               `json:"enabled,omitempty"`
	LogsEnabled    *bool               `json:"logs_enabled,omitempty"`
	Labels         *[]string           `json:"labels,omitempty"`
	Tags           *[]string           `json:"tags,omitempty"`
}

// CreateAPIKeyRequest is the payload for creating a new API key.
type CreateAPIKeyRequest struct {
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes"`
	CompoundID *string  `json:"compound_id,omitempty"` // scope key to a compound server
	ExpiresIn  *int     `json:"expires_in_days,omitempty"` // days from now
}

// CreateCompoundRequest is the payload for creating a compound server.
type CreateCompoundRequest struct {
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	MemberIDs      []string `json:"member_ids,omitempty"`
	DictionaryMode bool     `json:"dictionary_mode,omitempty"`
}

// UpdateCompoundRequest is the payload for updating a compound server.
type UpdateCompoundRequest struct {
	Name           *string  `json:"name,omitempty"`
	Description    *string  `json:"description,omitempty"`
	DictionaryMode *bool    `json:"dictionary_mode,omitempty"`
}

// LoginRequest is the payload for admin login.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// TokenResponse is the auth response with a JWT token.
type TokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ServerStatus represents the live status of a server.
type ServerStatus struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Tools    int    `json:"tools"`
	LastSeen string `json:"last_seen,omitempty"`
	Error    string `json:"error,omitempty"`
}

// DashboardStats represents summary statistics for the dashboard.
type DashboardStats struct {
	TotalServers     int `json:"total_servers"`
	ConnectedServers int `json:"connected_servers"`
	TotalTools       int `json:"total_tools"`
	TotalAPIKeys     int `json:"total_api_keys"`
	TotalCompounds   int `json:"total_compounds"`
	TotalMemories    int `json:"total_memories"`
	TotalSkills      int `json:"total_skills"`
	TotalTasks       int `json:"total_tasks"`
}

// Memory represents a stored memory in the built-in memory server.
// Inspired by MemPalace (spatial organization via palaces/rooms),
// Hindsight (importance scoring, access tracking), and Chronical
// memory (time-based chronicle with created/updated timestamps).
type Memory struct {
	ID           string     `json:"id"`
	SetID        string     `json:"set_id"`            // which memory set this belongs to
	Palace       string     `json:"palace"`            // top-level category (MemPalace)
	Room         string     `json:"room"`              // sub-category within a palace
	Content      string     `json:"content"`           // the memory text
	Tags         []string   `json:"tags"`              // searchable tags
	Importance   int        `json:"importance"`        // 0-100, hindsight-style scoring
	AccessCount  int        `json:"access_count"`      // times recalled (hindsight)
	CreatedAt    time.Time  `json:"created_at"`        // chronicle start
	UpdatedAt    time.Time  `json:"updated_at"`        // last modified
	LastAccessed *time.Time `json:"last_accessed,omitempty"` // last recalled (hindsight)
}

// MemorySet represents a named collection of memories for a specific project/org/context.
type MemorySet struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"` // URL-safe name used in tool namespace prefix
	Description string    `json:"description,omitempty"`
	IsDefault   bool      `json:"is_default"` // default set can't be deleted
	CreatedAt   time.Time `json:"created_at"`
}

// CreateMemorySetRequest is the payload for creating a memory set.
type CreateMemorySetRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// UpdateMemorySetRequest is the payload for updating a memory set.
type UpdateMemorySetRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

// CreateMemoryRequest is the payload for creating a memory.
type CreateMemoryRequest struct {
	Palace     string   `json:"palace"`
	Room       string   `json:"room,omitempty"`
	Content    string   `json:"content"`
	Tags       []string `json:"tags,omitempty"`
	Importance *int     `json:"importance,omitempty"`
}

// UpdateMemoryRequest is the payload for updating a memory.
type UpdateMemoryRequest struct {
	Palace     *string   `json:"palace,omitempty"`
	Room       *string   `json:"room,omitempty"`
	Content    *string   `json:"content,omitempty"`
	Tags       *[]string `json:"tags,omitempty"`
	Importance *int      `json:"importance,omitempty"`
}

// EnvVar represents an environment variable stored per project/environment.
type EnvVar struct {
	ID            string    `json:"id"`
	Project       string    `json:"project"`
	Environment   string    `json:"environment"` // e.g. "dev", "staging", "prod"
	Key           string    `json:"key"`
	Value         string    `json:"value"`             // plaintext (only in API responses)
	ResolvedValue string    `json:"resolved_value,omitempty"` // value with ${KEY} refs resolved
	IsReference   bool      `json:"is_reference"`       // true if value contains ${...} patterns
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// CreateEnvVarRequest creates a new env var.
type CreateEnvVarRequest struct {
	Project     string `json:"project"`
	Environment string `json:"environment"`
	Key         string `json:"key"`
	Value       string `json:"value"`
}

// UpdateEnvVarRequest updates an env var.
type UpdateEnvVarRequest struct {
	Value *string `json:"value,omitempty"`
}

// EnvVarExport is the encrypted response for API key auth.
type EnvVarExport struct {
	Project     string `json:"project"`
	Environment string `json:"environment"`
	Encrypted   string `json:"encrypted"`   // base64(nonce + ciphertext) — decrypt locally with API key
	Nonce       string `json:"nonce_hint"`  // first 8 chars of nonce for identification
}

// DisabledTool represents a tool that has been disabled (hidden from clients).
// If ServerID is nil, the disable is global; if set, it's scoped to a compound.
type DisabledTool struct {
	ID        string    `json:"id"`
	ToolName  string    `json:"tool_name"`           // namespaced tool name (serverName__toolName)
	ServerID  *string   `json:"server_id,omitempty"` // nil = global, set = compound-specific
	CreatedAt time.Time `json:"created_at"`
}

// Skill represents a reusable procedure stored in the proxy.
// Skills are exposed as MCP tools so LLM clients can discover and load them.
type Skill struct {
	ID          string    `json:"id"`
	SetID       string    `json:"set_id"`           // which skill set this belongs to
	Name        string    `json:"name"`             // unique, kebab-case (e.g. "deploy-dokploy")
	Description string    `json:"description"`     // short summary
	Content     string    `json:"content"`          // full SKILL.md body (markdown)
	Category    string    `json:"category"`         // grouping (e.g. "devops", "data-science")
	Tags        []string  `json:"tags"`             // searchable tags
	Version     string    `json:"version"`          // semver-ish (e.g. "1.0.0")
	AccessCount int       `json:"access_count"`     // times loaded (hindsight-style)
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	LastAccessed *time.Time `json:"last_accessed,omitempty"`
}

// SkillSet represents a named collection of skills for a specific project/org/context.
type SkillSet struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"` // URL-safe name used in tool namespace prefix
	Description string    `json:"description,omitempty"`
	IsDefault   bool      `json:"is_default"` // default set can't be deleted
	CreatedAt   time.Time `json:"created_at"`
}

// CreateSkillSetRequest is the payload for creating a skill set.
type CreateSkillSetRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// UpdateSkillSetRequest is the payload for updating a skill set.
type UpdateSkillSetRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

// CreateSkillRequest is the payload for creating a skill.
type CreateSkillRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Content     string   `json:"content"`
	Category    string   `json:"category,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Version     string   `json:"version,omitempty"`
}

// UpdateSkillRequest is the payload for updating a skill.
type UpdateSkillRequest struct {
	Name        *string   `json:"name,omitempty"`
	Description *string   `json:"description,omitempty"`
	Content     *string   `json:"content,omitempty"`
	Category    *string   `json:"category,omitempty"`
	Tags        *[]string `json:"tags,omitempty"`
	Version     *string   `json:"version,omitempty"`
}

// SkillCategory is a category with its skill count (for the UI filter pills).
type SkillCategory struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// BuiltinTaskBoardServerID is the virtual server ID for the built-in task board MCP server.
const BuiltinTaskBoardServerID = "builtin-tasks"

// TaskBoardSet represents a named task board (like a memory set or skill set).
// The default board is created automatically on first run.
type TaskBoardSet struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Description string    `json:"description,omitempty"`
	IsDefault   bool      `json:"is_default"`
	CreatedAt   time.Time `json:"created_at"`
}

// TaskItem represents a persistent task on the task board.
// Unlike the ephemeral MCP protocol tasks (internal/tasks), these are
// durable project-management tasks stored in SQLite — like a kanban board.
type TaskItem struct {
	ID            string     `json:"id"`
	BoardID       string     `json:"board_id"` // references TaskBoardSet.ID
	Title         string     `json:"title"`
	Description   string     `json:"description,omitempty"`
	Status        string     `json:"status"`        // "todo", "in_progress", "done", "blocked"
	Priority      string     `json:"priority"`      // "low", "medium", "high", "urgent"
	PriorityLevel int        `json:"priority_level"` // 1-5 (1=highest priority, 5=lowest)
	Assignee      string     `json:"assignee,omitempty"`
	DueDate       *time.Time `json:"due_date,omitempty"`
	Tags            []string   `json:"tags"`
	GithubIssueURL  string     `json:"github_issue_url,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// TaskBoardStats represents summary statistics for the task board dashboard.
type TaskBoardStats struct {
	Todo        int `json:"todo"`
	InProgress  int `json:"in_progress"`
	Done        int `json:"done"`
	Blocked     int `json:"blocked"`
	Total       int `json:"total"`
}

// CreateTaskItemRequest is the payload for creating a task board item.
type CreateTaskItemRequest struct {
	BoardID       string   `json:"board_id,omitempty"`
	Title         string   `json:"title"`
	Description   string   `json:"description,omitempty"`
	Status        string   `json:"status,omitempty"`
	Priority      string   `json:"priority,omitempty"`
	PriorityLevel *int     `json:"priority_level,omitempty"`
	Assignee       string   `json:"assignee,omitempty"`
	DueDate        *string  `json:"due_date,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	GithubIssueURL string   `json:"github_issue_url,omitempty"`
}

// UpdateTaskItemRequest is the payload for updating a task board item.
type UpdateTaskItemRequest struct {
	Title         *string  `json:"title,omitempty"`
	Description   *string  `json:"description,omitempty"`
	Status        *string  `json:"status,omitempty"`
	Priority      *string  `json:"priority,omitempty"`
	PriorityLevel *int     `json:"priority_level,omitempty"`
	Assignee       *string    `json:"assignee,omitempty"`
	DueDate        *string    `json:"due_date,omitempty"`
	Tags           *[]string  `json:"tags,omitempty"`
	GithubIssueURL *string    `json:"github_issue_url,omitempty"`
}

// GitHubAccount represents a stored GitHub account used for API authentication.
type GitHubAccount struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`       // display name, e.g. "Personal", "Work"
	Username  string    `json:"username"`   // GitHub username
	Token     string    `json:"-"`          // never serialized — write-only via API
	HasToken  bool      `json:"has_token"`  // indicates a token is set
	TokenEnv  string    `json:"token_env,omitempty"` // env var name for token (alternative to Token)
	CreatedAt time.Time `json:"created_at"`
}

// CreateGitHubAccountRequest is the payload for creating a new GitHub account.
type CreateGitHubAccountRequest struct {
	Name     string `json:"name"`
	Username string `json:"username"`
	Token    string `json:"token,omitempty"`
	TokenEnv string `json:"token_env,omitempty"`
}
