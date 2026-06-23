package models

import (
	"time"
)

// Server represents a backend MCP server that the proxy connects to.
type Server struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Transport      string     `json:"transport"` // "stdio", "http", or "streamable-http"
	Command        string     `json:"command,omitempty"`
	Args           []string   `json:"args,omitempty"`
	URL            string     `json:"url,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	AuthToken      string     `json:"auth_token,omitempty"`
	Timeout        int        `json:"timeout"`
	ConnectTimeout int        `json:"connect_timeout"`
	Enabled        bool       `json:"enabled"`
	Status         string     `json:"status"` // "connected", "disconnected", "error"
	LastSeen       *time.Time `json:"last_seen,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
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
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// CompoundServerWithMembers includes the member server IDs.
type CompoundServerWithMembers struct {
	CompoundServer
	Members     []Server `json:"members"`
	ToolCount   int       `json:"tool_count"`
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
	Timeout        int                 `json:"timeout,omitempty"`
	ConnectTimeout int                 `json:"connect_timeout,omitempty"`
	Enabled        *bool               `json:"enabled,omitempty"`
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
	Timeout        *int                `json:"timeout,omitempty"`
	ConnectTimeout *int                `json:"connect_timeout,omitempty"`
	Enabled        *bool               `json:"enabled,omitempty"`
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
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	MemberIDs   []string `json:"member_ids,omitempty"`
}

// UpdateCompoundRequest is the payload for updating a compound server.
type UpdateCompoundRequest struct {
	Name        *string  `json:"name,omitempty"`
	Description *string  `json:"description,omitempty"`
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
}

// Memory represents a stored memory in the built-in memory server.
// Inspired by MemPalace (spatial organization via palaces/rooms),
// Hindsight (importance scoring, access tracking), and Chronical
// memory (time-based chronicle with created/updated timestamps).
type Memory struct {
	ID          string     `json:"id"`
	Palace      string     `json:"palace"`            // top-level category (MemPalace)
	Room        string     `json:"room"`             // sub-category within a palace
	Content     string     `json:"content"`          // the memory text
	Tags        []string   `json:"tags"`             // searchable tags
	Importance  int        `json:"importance"`        // 0-100, hindsight-style scoring
	AccessCount int        `json:"access_count"`      // times recalled (hindsight)
	CreatedAt   time.Time  `json:"created_at"`        // chronicle start
	UpdatedAt   time.Time  `json:"updated_at"`        // last modified
	LastAccessed *time.Time `json:"last_accessed,omitempty"` // last recalled (hindsight)
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
	Palace     *string  `json:"palace,omitempty"`
	Room       *string  `json:"room,omitempty"`
	Content    *string  `json:"content,omitempty"`
	Tags       *[]string `json:"tags,omitempty"`
	Importance *int     `json:"importance,omitempty"`
}
