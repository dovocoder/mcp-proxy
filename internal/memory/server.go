package memory

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

// Server is the built-in memory MCP server.
// It exposes tools for storing, recalling, searching, and managing
// persistent memories — inspired by:
//   - MemPalace: spatial organization via "palaces" and "rooms"
//   - Hindsight: importance scoring, access tracking, reflection
//   - Chronical memory: time-based chronicle with timestamps
type Server struct {
	store *store.Store
	setID string
	slug  string
}

// New creates a new built-in memory server bound to a specific memory set.
func New(s *store.Store, setID, slug string) *Server {
	if setID == "" {
		setID = "default"
	}
	return &Server{store: s, setID: setID, slug: slug}
}

// SetID returns the memory set ID this server is bound to.
func (s *Server) SetID() string {
	return s.setID
}

// Slug returns the slug of the memory set this server is bound to.
func (s *Server) Slug() string {
	return s.slug
}

// Tools returns the MCP tool definitions exposed by the memory server.
func (s *Server) Tools() []mcp.Tool {
	return []mcp.Tool{
		{
			Name:        "memory_store",
			Description: "Store a new memory. Memories are organized into 'palaces' (top-level categories) and 'rooms' (sub-categories), inspired by the memory palace technique. Use meaningful palace names like 'projects', 'decisions', 'learnings', 'context'. Importance (0-100) controls recall priority — higher values surface first.",
			InputSchema: mustJSON(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"palace": map[string]interface{}{
						"type":        "string",
						"description": "Top-level category for this memory (e.g. 'projects', 'decisions', 'learnings')",
						"default":     "general",
					},
					"room": map[string]interface{}{
						"type":        "string",
						"description": "Sub-category within the palace (optional)",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "The memory content to store",
					},
					"tags": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Tags for searchability",
					},
					"importance": map[string]interface{}{
						"type":        "integer",
						"description": "Priority score 0-100 (default: 50). Higher = recalled first.",
						"minimum":     0,
						"maximum":     100,
					},
				},
				"required": []string{"content"},
			}),
		},
		{
			Name:        "memory_recall",
			Description: "Recall memories from a specific palace, optionally filtered by room. Returns memories sorted by importance (highest first). Recalling a memory increments its access count (hindsight-style tracking).",
			InputSchema: mustJSON(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"palace": map[string]interface{}{
						"type":        "string",
						"description": "The palace to recall from. Omit to list across all palaces.",
					},
					"room": map[string]interface{}{
						"type":        "string",
						"description": "Optional room filter within the palace",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Max memories to return (default: 20)",
						"default":     20,
					},
				},
			}),
		},
		{
			Name:        "memory_search",
			Description: "Search across all memories by keyword. Matches against content and tags. Results are sorted by importance and recency.",
			InputSchema: mustJSON(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Search query",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Max results (default: 20)",
						"default":     20,
					},
				},
				"required": []string{"query"},
			}),
		},
		{
			Name:        "memory_update",
			Description: "Update an existing memory's content, tags, palace, room, or importance. Useful for refining memories over time.",
			InputSchema: mustJSON(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Memory ID to update",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "New content",
					},
					"tags": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "New tags",
					},
					"palace": map[string]interface{}{
						"type":        "string",
						"description": "Move to a different palace",
					},
					"room": map[string]interface{}{
						"type":        "string",
						"description": "Move to a different room",
					},
					"importance": map[string]interface{}{
						"type":        "integer",
						"description": "New importance score 0-100",
					},
				},
				"required": []string{"id"},
			}),
		},
		{
			Name:        "memory_delete",
			Description: "Delete a memory by ID. This is permanent.",
			InputSchema: mustJSON(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Memory ID to delete",
					},
				},
				"required": []string{"id"},
			}),
		},
		{
			Name:        "memory_reflect",
			Description: "Get an overview of memory usage patterns. Shows palace distribution, most-accessed memories, and chronicle timeline. Inspired by hindsight reflection — understand what memories are being used most.",
			InputSchema: mustJSON(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{},
			}),
		},
	}
}

// HandleToolCall processes a memory tool call and returns the result
// wrapped in MCP content format: {content: [{type: "text", text: "..."}]}.
func (s *Server) HandleToolCall(toolName string, args json.RawMessage) (json.RawMessage, error) {
	var result interface{}
	var err error

	switch toolName {
	case "memory_store":
		result, err = s.handleStore(args)
	case "memory_recall":
		result, err = s.handleRecall(args)
	case "memory_search":
		result, err = s.handleSearch(args)
	case "memory_update":
		result, err = s.handleUpdate(args)
	case "memory_delete":
		result, err = s.handleDelete(args)
	case "memory_reflect":
		result, err = s.handleReflect(args)
	default:
		return nil, fmt.Errorf("unknown memory tool: %s", toolName)
	}

	if err != nil {
		return nil, err
	}

	// Wrap in MCP content format
	textBytes, _ := json.Marshal(result)
	return json.Marshal(map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": string(textBytes),
			},
		},
	})
}

func (s *Server) handleStore(args json.RawMessage) (interface{}, error) {
	var params struct {
		Palace     string   `json:"palace"`
		Room       string   `json:"room"`
		Content    string   `json:"content"`
		Tags       []string `json:"tags"`
		Importance *int     `json:"importance"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Content == "" {
		return nil, fmt.Errorf("content is required")
	}
	if params.Palace == "" {
		params.Palace = "general"
	}
	importance := 50
	if params.Importance != nil {
		importance = *params.Importance
	}
	if params.Tags == nil {
		params.Tags = []string{}
	}
	now := time.Now()
	mem := &models.Memory{
		ID:         "mem_" + uuid.NewString(),
		SetID:      s.setID,
		Palace:     params.Palace,
		Room:       params.Room,
		Content:    params.Content,
		Tags:       params.Tags,
		Importance: importance,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.store.CreateMemory(mem); err != nil {
		return nil, fmt.Errorf("failed to store memory: %w", err)
	}
	return map[string]interface{}{
		"id":        mem.ID,
		"palace":    mem.Palace,
		"room":      mem.Room,
		"message":   "Memory stored successfully",
		"chronicle": "Created at " + now.Format(time.RFC3339),
	}, nil
}

func (s *Server) handleRecall(args json.RawMessage) (interface{}, error) {
	var params struct {
		Palace string `json:"palace"`
		Room   string `json:"room"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Limit == 0 {
		params.Limit = 20
	}
	memories, err := s.store.ListMemories(s.setID, params.Palace)
	if err != nil {
		return nil, fmt.Errorf("failed to recall memories: %w", err)
	}
	// Filter by room if specified
	var filtered []*models.Memory
	for _, m := range memories {
		if params.Room != "" && m.Room != params.Room {
			continue
		}
		// Hindsight: touch access count on recall
		_ = s.store.TouchMemory(m.ID)
		filtered = append(filtered, m)
		if len(filtered) >= params.Limit {
			break
		}
	}
	return map[string]interface{}{
		"memories": filtered,
		"count":    len(filtered),
	}, nil
}

func (s *Server) handleSearch(args json.RawMessage) (interface{}, error) {
	var params struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if params.Limit == 0 {
		params.Limit = 20
	}
	memories, err := s.store.SearchMemories(s.setID, params.Query)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}
	if len(memories) > params.Limit {
		memories = memories[:params.Limit]
	}
	return map[string]interface{}{
		"memories": memories,
		"count":    len(memories),
		"query":    params.Query,
	}, nil
}

func (s *Server) handleUpdate(args json.RawMessage) (interface{}, error) {
	var params struct {
		models.UpdateMemoryRequest
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	if err := s.store.UpdateMemory(params.ID, &params.UpdateMemoryRequest); err != nil {
		return nil, fmt.Errorf("failed to update memory: %w", err)
	}
	mem, _ := s.store.GetMemory(params.ID)
	return map[string]interface{}{
		"memory":  mem,
		"message": "Memory updated successfully",
	}, nil
}

func (s *Server) handleDelete(args json.RawMessage) (interface{}, error) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if err := s.store.DeleteMemory(params.ID); err != nil {
		return nil, fmt.Errorf("failed to delete memory: %w", err)
	}
	return map[string]interface{}{
		"id":      params.ID,
		"message": "Memory deleted",
	}, nil
}

func (s *Server) handleReflect(args json.RawMessage) (interface{}, error) {
	palaces, err := s.store.ListPalaces(s.setID)
	if err != nil {
		return nil, fmt.Errorf("failed to list palaces: %w", err)
	}
	allMemories, err := s.store.ListMemories(s.setID, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list memories: %w", err)
	}

	// Chronicle: group by date
	chronicle := map[string]int{}
	// Hindsight: most accessed
	var topAccessed []*models.Memory
	topAccessed = append(topAccessed, allMemories...)
	// Sort by access count (simple selection — just take top 5)
	for i := 0; i < len(topAccessed) && i < 5; i++ {
		maxIdx := i
		for j := i + 1; j < len(topAccessed); j++ {
			if topAccessed[j].AccessCount > topAccessed[maxIdx].AccessCount {
				maxIdx = j
			}
		}
		topAccessed[i], topAccessed[maxIdx] = topAccessed[maxIdx], topAccessed[i]
	}
	if len(topAccessed) > 5 {
		topAccessed = topAccessed[:5]
	}

	for _, m := range allMemories {
		date := m.CreatedAt.Format("2006-01-02")
		chronicle[date]++
	}

	// Collect all unique tags
	tagCounts := map[string]int{}
	for _, m := range allMemories {
		for _, t := range m.Tags {
			tagCounts[t]++
		}
	}

	return map[string]interface{}{
		"total_memories": len(allMemories),
		"palaces":        palaces,
		"chronicle":      chronicle,
		"top_accessed":   topAccessed,
		"tag_counts":     tagCounts,
	}, nil
}

// ToolNameList returns just the tool names (for quick lookup).
func (s *Server) ToolNames() map[string]bool {
	names := make(map[string]bool, len(s.Tools()))
	for _, t := range s.Tools() {
		names[t.Name] = true
	}
	return names
}

// IsMemoryTool returns true if the given tool name is a built-in memory tool.
func (s *Server) IsMemoryTool(name string) bool {
	return s.ToolNames()[name]
}

// NamespacePrefix returns the prefix for this set's tools.
// Default set (slug=""): "memory__"
// Custom set (slug="project_a"): "memory_project_a__"
func (s *Server) NamespacePrefix() string {
	if s.slug == "" {
		return "memory__"
	}
	return "memory_" + s.slug + "__"
}

// NamespacedName returns the tool name with this set's namespace prefix.
func (s *Server) NamespacedName(toolName string) string {
	return s.NamespacePrefix() + toolName
}

// ParseNamespaced splits a memory-namespaced tool name.
// Returns (setSlug, toolName, ok).
// "memory__memory_store" → ("", "memory_store", true) — default set
// "memory_projecta__memory_store" → ("projecta", "memory_store", true) — custom set
func ParseNamespaced(namespaced string) (setSlug string, base string, ok bool) {
	if strings.HasPrefix(namespaced, "memory__") {
		return "", strings.TrimPrefix(namespaced, "memory__"), true
	}
	if strings.HasPrefix(namespaced, "memory_") {
		rest := strings.TrimPrefix(namespaced, "memory_")
		idx := strings.Index(rest, "__")
		if idx > 0 {
			return rest[:idx], rest[idx+2:], true
		}
	}
	return "", "", false
}

// NamespacedNameFor creates a namespaced tool name for a given slug and tool name.
func NamespacedNameFor(slug, toolName string) string {
	if slug == "" {
		return "memory__" + toolName
	}
	return "memory_" + slug + "__" + toolName
}

// IsMemoryServerID returns true if the server ID refers to a builtin memory server.
// "builtin-memory" → true (default set)
// "builtin-memory:xxx" → true (custom set)
func IsMemoryServerID(serverID string) bool {
	return serverID == models.BuiltinMemoryServerID ||
		strings.HasPrefix(serverID, models.BuiltinMemoryServerID+":")
}

// MemorySetIDFromServerID extracts the set ID from a memory server ID.
// "builtin-memory" → "default"
// "builtin-memory:abc123" → "abc123"
func MemorySetIDFromServerID(serverID string) string {
	if serverID == models.BuiltinMemoryServerID {
		return "default"
	}
	if strings.HasPrefix(serverID, models.BuiltinMemoryServerID+":") {
		return strings.TrimPrefix(serverID, models.BuiltinMemoryServerID+":")
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
