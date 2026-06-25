package skills

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/agentic/mcp-proxy/internal/mcp"
	"github.com/agentic/mcp-proxy/internal/models"
	"github.com/agentic/mcp-proxy/internal/store"
	"github.com/google/uuid"
)

// Server is the built-in skill MCP server.
// It exposes tools for listing, loading, creating, and managing
// reusable skills (procedural knowledge documents).
type Server struct {
	store *store.Store
	setID string
	slug  string
}

// New creates a new built-in skill server bound to a specific skill set.
func New(s *store.Store, setID, slug string) *Server {
	if setID == "" {
		setID = "default"
	}
	return &Server{store: s, setID: setID, slug: slug}
}

// SetID returns the skill set ID this server is bound to.
func (s *Server) SetID() string {
	return s.setID
}

// Slug returns the slug of the skill set this server is bound to.
func (s *Server) Slug() string {
	return s.slug
}

// Tools returns the MCP tool definitions exposed by the skill server.
func (s *Server) Tools() []mcp.Tool {
	return []mcp.Tool{
		{
			Name:        "skill_list",
			Title:       "List Skills",
			Description: "List all available skills. Returns skill names, descriptions, categories, and versions. Use this first to discover what skills exist before loading one.",
			InputSchema: mustJSON(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"category": map[string]interface{}{
						"type":        "string",
						"description": "Filter by category (optional)",
					},
				},
			}),
		},
		{
			Name:        "skill_load",
			Title:       "Load Skill",
			Description: "Load the full content of a skill by name. Returns the complete SKILL.md body — instructions, steps, commands, and pitfalls. Always call this before executing a skill's procedure.",
			InputSchema: mustJSON(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "The skill name (kebab-case, e.g. 'deploy-dokploy')",
					},
				},
				"required": []string{"name"},
			}),
		},
		{
			Name:        "skill_search",
			Title:       "Search Skills",
			Description: "Search skills by keyword. Matches against name, description, content, and tags. Use this when you're not sure which skill exists for a task.",
			InputSchema: mustJSON(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Search query",
					},
				},
				"required": []string{"query"},
			}),
		},
		{
			Name:        "skill_create",
			Title:       "Create Skill",
			Description: "Create a new skill. Skills are reusable procedures (SKILL.md format). Include: trigger conditions, numbered steps with exact commands, pitfalls, and verification steps. Search first to avoid duplicates.",
			InputSchema: mustJSON(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Unique kebab-case name (e.g. 'deploy-dokploy')",
					},
					"description": map[string]interface{}{
						"type":        "string",
						"description": "Short summary of what the skill does",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "Full SKILL.md body (markdown)",
					},
					"category": map[string]interface{}{
						"type":        "string",
						"description": "Grouping (e.g. 'devops', 'data-science')",
						"default":     "general",
					},
					"tags": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Searchable tags",
					},
					"version": map[string]interface{}{
						"type":        "string",
						"description": "Version string (e.g. '1.0.0')",
						"default":     "1.0.0",
					},
				},
				"required": []string{"name", "content"},
			}),
		},
		{
			Name:        "skill_update",
			Title:       "Update Skill",
			Description: "Update an existing skill's content, description, tags, or version. Useful for refining skills after learning new pitfalls.",
			InputSchema: mustJSON(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Skill ID to update",
					},
					"description": map[string]interface{}{
						"type":        "string",
						"description": "New description",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "New content",
					},
					"category": map[string]interface{}{
						"type":        "string",
						"description": "New category",
					},
					"tags": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "New tags",
					},
					"version": map[string]interface{}{
						"type":        "string",
						"description": "New version",
					},
				},
				"required": []string{"id"},
			}),
		},
		{
			Name:        "skill_delete",
			Title:       "Delete Skill",
			Description: "Delete a skill by ID. This is permanent.",
			InputSchema: mustJSON(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Skill ID to delete",
					},
				},
				"required": []string{"id"},
			}),
		},
	}
}

// HandleToolCall processes a skill tool call and returns the result
// wrapped in MCP content format with isError field.
func (s *Server) HandleToolCall(toolName string, args json.RawMessage) (json.RawMessage, error) {
	var result interface{}
	var err error

	switch toolName {
	case "skill_list":
		result, err = s.handleList(args)
	case "skill_load":
		result, err = s.handleLoad(args)
	case "skill_search":
		result, err = s.handleSearch(args)
	case "skill_create":
		result, err = s.handleCreate(args)
	case "skill_update":
		result, err = s.handleUpdate(args)
	case "skill_delete":
		result, err = s.handleDelete(args)
	default:
		// Unknown tool — protocol error (not a tool execution error)
		return nil, fmt.Errorf("unknown skill tool: %s", toolName)
	}

	if err != nil {
		// Tool execution errors should be returned as isError: true,
		// not as JSON-RPC errors. This allows the LLM to self-correct.
		return wrapMCPError(err.Error())
	}

	return wrapMCPContent(result)
}

// wrapMCPContent wraps a successful result in MCP content format.
func wrapMCPContent(result interface{}) (json.RawMessage, error) {
	textBytes, _ := json.Marshal(result)
	return json.Marshal(map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": string(textBytes),
			},
		},
		"isError": false,
	})
}

// wrapMCPError wraps a tool execution error in MCP content format.
func wrapMCPError(message string) (json.RawMessage, error) {
	return json.Marshal(map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": message,
			},
		},
		"isError": true,
	})
}

func (s *Server) handleList(args json.RawMessage) (interface{}, error) {
	var params struct {
		Category string `json:"category"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &params); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	skills, err := s.store.ListSkills(s.setID, params.Category)
	if err != nil {
		return nil, fmt.Errorf("failed to list skills: %w", err)
	}
	// Return lightweight listing (no full content)
	type skillListItem struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Category    string   `json:"category"`
		Tags        []string `json:"tags"`
		Version     string   `json:"version"`
		UpdatedAt   string   `json:"updated_at"`
	}
	var items []skillListItem
	for _, sk := range skills {
		items = append(items, skillListItem{
			ID:          sk.ID,
			Name:        sk.Name,
			Description: sk.Description,
			Category:    sk.Category,
			Tags:        sk.Tags,
			Version:     sk.Version,
			UpdatedAt:   sk.UpdatedAt.Format(time.RFC3339),
		})
	}
	return map[string]interface{}{
		"skills": items,
		"count":  len(items),
	}, nil
}

func (s *Server) handleLoad(args json.RawMessage) (interface{}, error) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	sk, err := s.store.GetSkillByName(s.setID, params.Name)
	if err != nil {
		return nil, fmt.Errorf("skill not found: %s", params.Name)
	}
	// Touch access count
	_ = s.store.TouchSkill(sk.ID)
	return map[string]interface{}{
		"skill": sk,
	}, nil
}

func (s *Server) handleSearch(args json.RawMessage) (interface{}, error) {
	var params struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Query == "" {
		return nil, fmt.Errorf("query is required")
	}
	skills, err := s.store.SearchSkills(s.setID, params.Query)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}
	// Return lightweight listing
	type skillSearchItem struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Category    string   `json:"category"`
		Tags        []string `json:"tags"`
	}
	var items []skillSearchItem
	for _, sk := range skills {
		items = append(items, skillSearchItem{
			ID:          sk.ID,
			Name:        sk.Name,
			Description: sk.Description,
			Category:    sk.Category,
			Tags:        sk.Tags,
		})
	}
	return map[string]interface{}{
		"results": items,
		"count":   len(items),
		"query":   params.Query,
	}, nil
}

func (s *Server) handleCreate(args json.RawMessage) (interface{}, error) {
	var params struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Content     string   `json:"content"`
		Category    string   `json:"category"`
		Tags        []string `json:"tags"`
		Version     string   `json:"version"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if params.Content == "" {
		return nil, fmt.Errorf("content is required")
	}
	if params.Category == "" {
		params.Category = "general"
	}
	if params.Version == "" {
		params.Version = "1.0.0"
	}
	if params.Tags == nil {
		params.Tags = []string{}
	}
	sk := &models.Skill{
		ID:          "skill_" + uuid.NewString(),
		SetID:       s.setID,
		Name:        params.Name,
		Description: params.Description,
		Content:     params.Content,
		Category:    params.Category,
		Tags:        params.Tags,
		Version:     params.Version,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := s.store.CreateSkill(sk); err != nil {
		return nil, fmt.Errorf("failed to create skill: %w", err)
	}
	return map[string]interface{}{
		"id":      sk.ID,
		"name":    sk.Name,
		"message": "Skill created successfully",
	}, nil
}

func (s *Server) handleUpdate(args json.RawMessage) (interface{}, error) {
	var params struct {
		models.UpdateSkillRequest
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	sk, err := s.store.GetSkill(params.ID)
	if err != nil {
		return nil, fmt.Errorf("skill not found: %s", params.ID)
	}
	// Apply partial updates
	if params.Description != nil {
		sk.Description = *params.Description
	}
	if params.Content != nil {
		sk.Content = *params.Content
	}
	if params.Category != nil {
		sk.Category = *params.Category
	}
	if params.Tags != nil {
		sk.Tags = *params.Tags
	}
	if params.Version != nil {
		sk.Version = *params.Version
	}
	if err := s.store.UpdateSkill(sk); err != nil {
		return nil, fmt.Errorf("failed to update skill: %w", err)
	}
	return map[string]interface{}{
		"skill":   sk,
		"message": "Skill updated successfully",
	}, nil
}

func (s *Server) handleDelete(args json.RawMessage) (interface{}, error) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if err := s.store.DeleteSkill(params.ID); err != nil {
		return nil, fmt.Errorf("failed to delete skill: %w", err)
	}
	return map[string]interface{}{
		"id":      params.ID,
		"message": "Skill deleted",
	}, nil
}

// ToolNames returns just the tool names (for quick lookup).
func (s *Server) ToolNames() map[string]bool {
	names := make(map[string]bool, len(s.Tools()))
	for _, t := range s.Tools() {
		names[t.Name] = true
	}
	return names
}

// IsSkillTool returns true if the given tool name is a built-in skill tool.
func (s *Server) IsSkillTool(name string) bool {
	return s.ToolNames()[name]
}

// NamespaceSuffix returns the suffix for this set's tools.
// Default set (slug=""): "" — tool name stays as-is (e.g. "skill_list")
// Custom set (slug="project_a"): "__project_a" — tool name becomes "skill_list__project_a"
func (s *Server) NamespaceSuffix() string {
	if s.slug == "" {
		return ""
	}
	return "__" + s.slug
}

// NamespacedName returns the tool name with this set's suffix appended.
func (s *Server) NamespacedName(toolName string) string {
	return toolName + s.NamespaceSuffix()
}

// ParseNamespaced splits a skill-namespaced tool name.
// Returns (setSlug, toolName, ok).
// "skill_list" → ("", "skill_list", true) — default set (no suffix)
// "skill_list__work" → ("work", "skill_list", true) — custom set
func ParseNamespaced(namespaced string) (setSlug string, base string, ok bool) {
	if !strings.HasPrefix(namespaced, "skill_") {
		return "", "", false
	}
	knownTools := []string{"skill_list", "skill_load", "skill_search", "skill_create", "skill_update", "skill_delete"}
	for _, kt := range knownTools {
		if namespaced == kt {
			return "", kt, true // default set, no suffix
		}
		if strings.HasPrefix(namespaced, kt+"__") {
			slug := strings.TrimPrefix(namespaced, kt+"__")
			if slug != "" {
				return slug, kt, true
			}
		}
	}
	return "", "", false
}

// NamespacedNameFor creates a namespaced tool name for a given slug and tool name.
func NamespacedNameFor(slug, toolName string) string {
	if slug == "" {
		return toolName
	}
	return toolName + "__" + slug
}

// IsSkillServerID returns true if the server ID refers to a builtin skill server.
// "builtin-skills" → true (default set)
// "builtin-skills:xxx" → true (custom set)
func IsSkillServerID(serverID string) bool {
	return serverID == models.BuiltinSkillServerID ||
		strings.HasPrefix(serverID, models.BuiltinSkillServerID+":")
}

// SkillSetIDFromServerID extracts the set ID from a skill server ID.
// "builtin-skills" → "default"
// "builtin-skills:abc123" → "abc123"
func SkillSetIDFromServerID(serverID string) string {
	if serverID == models.BuiltinSkillServerID {
		return "default"
	}
	if strings.HasPrefix(serverID, models.BuiltinSkillServerID+":") {
		return strings.TrimPrefix(serverID, models.BuiltinSkillServerID+":")
	}
	return "default"
}

func mustJSON(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
