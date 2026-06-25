package taskboard

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/agentic/mcp-proxy/internal/mcp"
	"github.com/agentic/mcp-proxy/internal/models"
	"github.com/agentic/mcp-proxy/internal/store"
)

// Server is the built-in task board MCP server.
// It exposes tools for managing a persistent kanban-style task board,
// allowing LLM clients to create, update, list, and manage tasks.
type Server struct {
	store *store.Store
}

// New creates a new built-in task board server.
func New(s *store.Store) *Server {
	return &Server{store: s}
}

// Tools returns the MCP tool definitions exposed by the task board server.
func (s *Server) Tools() []mcp.Tool {
	return []mcp.Tool{
		{
			Name:        "task_create",
			Title:       "Create Task",
			Description: "Create a new task on the task board. Tasks are persistent (unlike ephemeral MCP protocol tasks). Use this to track work items, TODOs, and action items. Status: todo/in_progress/done/blocked. Priority: low/medium/high/urgent. Priority level: 1-5 (1=highest). Board: optional board_id to create on a specific board.",
			InputSchema: mustJSON(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"board_id": map[string]interface{}{
						"type":        "string",
						"description": "Task board ID (optional, defaults to 'default')",
					},
					"title": map[string]interface{}{
						"type":        "string",
						"description": "Task title (required)",
					},
					"description": map[string]interface{}{
						"type":        "string",
						"description": "Detailed task description (optional)",
					},
					"status": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"todo", "in_progress", "done", "blocked"},
						"default":     "todo",
						"description": "Initial status (default: todo)",
					},
					"priority": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"low", "medium", "high", "urgent"},
						"default":     "medium",
						"description": "Task priority (default: medium)",
					},
					"priority_level": map[string]interface{}{
						"type":        "integer",
						"minimum":     1,
						"maximum":     5,
						"default":     3,
						"description": "Numeric priority level 1-5 (1=highest, 5=lowest). Used for ordering within a priority category.",
					},
					"assignee": map[string]interface{}{
						"type":        "string",
						"description": "Who is assigned to this task (optional)",
					},
					"tags": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Tags for categorization (optional)",
					},
				},
				"required": []string{"title"},
			}),
		},
		{
			Name:        "task_list",
			Title:       "List Tasks",
			Description: "List tasks on the task board. Optionally filter by board, status, or priority. Returns tasks sorted by priority (urgent first) then priority level then creation date.",
			InputSchema: mustJSON(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"board_id": map[string]interface{}{
						"type":        "string",
						"description": "Filter by task board ID (optional, omit for all boards)",
					},
					"status": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"todo", "in_progress", "done", "blocked"},
						"description": "Filter by status (optional)",
					},
					"priority": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"low", "medium", "high", "urgent"},
						"description": "Filter by priority (optional)",
					},
				},
			}),
		},
		{
			Name:        "task_get",
			Title:       "Get Task",
			Description: "Get a single task by its ID. Returns full details including description, assignee, tags, and timestamps.",
			InputSchema: mustJSON(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Task ID (e.g. task_abc123)",
					},
				},
				"required": []string{"id"},
			}),
		},
		{
			Name:        "task_update",
			Title:       "Update Task",
			Description: "Update an existing task. Any field can be updated — status transitions, priority changes, reassignment, etc. Use this to mark tasks as done, block them, or change priority.",
			InputSchema: mustJSON(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Task ID (required)",
					},
					"title": map[string]interface{}{"type": "string"},
					"description": map[string]interface{}{"type": "string"},
					"status": map[string]interface{}{
						"type": "string",
						"enum": []string{"todo", "in_progress", "done", "blocked"},
					},
					"priority": map[string]interface{}{
						"type": "string",
						"enum": []string{"low", "medium", "high", "urgent"},
					},
					"priority_level": map[string]interface{}{
						"type":        "integer",
						"minimum":     1,
						"maximum":     5,
						"description": "Numeric priority level 1-5 (1=highest)",
					},
					"assignee": map[string]interface{}{"type": "string"},
					"tags": map[string]interface{}{
						"type":  "array",
						"items": map[string]interface{}{"type": "string"},
					},
				},
				"required": []string{"id"},
			}),
		},
		{
			Name:        "task_delete",
			Title:       "Delete Task",
			Description: "Delete a task permanently by ID. This cannot be undone.",
			InputSchema: mustJSON(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Task ID to delete",
					},
				},
				"required": []string{"id"},
			}),
		},
		{
			Name:        "task_search",
			Title:       "Search Tasks",
			Description: "Search tasks by keyword. Matches against title, description, assignee, and tags. Use this to find tasks when you don't know the ID.",
			InputSchema: mustJSON(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Search query (matches title, description, assignee, tags)",
					},
				},
				"required": []string{"query"},
			}),
		},
	}
}

// NamespacedName returns the tool name with optional slug suffix.
// For the default task board, tools are not namespaced (no sets).
func NamespacedName(toolName string) string {
	return toolName // task board has a single global set — no namespacing needed
}

// ParseNamespaced checks if a tool name belongs to the task board.
func ParseNamespaced(name string) (string, bool) {
	knownTools := []string{"task_create", "task_list", "task_get", "task_update", "task_delete", "task_search"}
	for _, kt := range knownTools {
		if name == kt {
			return kt, true
		}
	}
	return "", false
}

// HandleToolCall dispatches a tool call to the appropriate handler.
func (s *Server) HandleToolCall(name string, args json.RawMessage) (json.RawMessage, error) {
	switch name {
	case "task_create":
		return s.handleCreate(args)
	case "task_list":
		return s.handleList(args)
	case "task_get":
		return s.handleGet(args)
	case "task_update":
		return s.handleUpdate(args)
	case "task_delete":
		return s.handleDelete(args)
	case "task_search":
		return s.handleSearch(args)
	default:
		return nil, fmt.Errorf("unknown task board tool: %s", name)
	}
}

func (s *Server) handleCreate(args json.RawMessage) (json.RawMessage, error) {
	var params struct {
		BoardID       string   `json:"board_id"`
		Title         string   `json:"title"`
		Description   string   `json:"description"`
		Status        string   `json:"status"`
		Priority      string   `json:"priority"`
		PriorityLevel int      `json:"priority_level"`
		Assignee      string   `json:"assignee"`
		Tags          []string `json:"tags"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if params.Tags == nil {
		params.Tags = []string{}
	}
	task := &models.TaskItem{
		BoardID:       params.BoardID,
		Title:         params.Title,
		Description:   params.Description,
		Status:        params.Status,
		Priority:      params.Priority,
		PriorityLevel: params.PriorityLevel,
		Assignee:      params.Assignee,
		Tags:          params.Tags,
	}
	if err := s.store.CreateTaskItem(task); err != nil {
		return nil, fmt.Errorf("failed to create task: %w", err)
	}
	return json.Marshal(map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": fmt.Sprintf("Created task **%s** (ID: %s)\nBoard: %s\nStatus: %s | Priority: %s (level %d)", task.Title, task.ID, task.BoardID, task.Status, task.Priority, task.PriorityLevel)},
		},
		"isError": false,
	})
}

func (s *Server) handleList(args json.RawMessage) (json.RawMessage, error) {
	var params struct {
		BoardID   string `json:"board_id"`
		Status   string `json:"status"`
		Priority string `json:"priority"`
	}
	json.Unmarshal(args, &params)
	tasks, err := s.store.ListTaskItems(params.BoardID, params.Status, params.Priority)
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d task(s):\n\n", len(tasks)))
	for _, t := range tasks {
		sb.WriteString(fmt.Sprintf("- **%s** [%s] (%s L%d) — ID: %s", t.Title, t.Status, t.Priority, t.PriorityLevel, t.ID))
		if t.Assignee != "" {
			sb.WriteString(fmt.Sprintf(" @%s", t.Assignee))
		}
		if len(t.Tags) > 0 {
			sb.WriteString(" [" + strings.Join(t.Tags, ", ") + "]")
		}
		sb.WriteString("\n")
	}
	return json.Marshal(map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": sb.String()},
		},
		"isError": false,
	})
}

func (s *Server) handleGet(args json.RawMessage) (json.RawMessage, error) {
	var params struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &params)
	if params.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	task, err := s.store.GetTaskItem(params.ID)
	if err != nil {
		return nil, fmt.Errorf("task not found: %w", err)
	}
	return json.Marshal(map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": fmt.Sprintf("**%s**\n\nID: %s\nBoard: %s\nStatus: %s\nPriority: %s (level %d)\nAssignee: %s\nTags: %s\nCreated: %s\nUpdated: %s\n\n%s",
				task.Title, task.ID, task.BoardID, task.Status, task.Priority, task.PriorityLevel, task.Assignee,
				strings.Join(task.Tags, ", "),
				task.CreatedAt.Format(time.RFC3339), task.UpdatedAt.Format(time.RFC3339),
				task.Description)},
		},
		"isError": false,
	})
}

func (s *Server) handleUpdate(args json.RawMessage) (json.RawMessage, error) {
	var params struct {
		ID            string   `json:"id"`
		Title         *string  `json:"title"`
		Description   *string  `json:"description"`
		Status        *string  `json:"status"`
		Priority      *string  `json:"priority"`
		PriorityLevel *int     `json:"priority_level"`
		Assignee      *string  `json:"assignee"`
		Tags          *[]string `json:"tags"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	task, err := s.store.GetTaskItem(params.ID)
	if err != nil {
		return nil, fmt.Errorf("task not found: %w", err)
	}
	if params.Title != nil {
		task.Title = *params.Title
	}
	if params.Description != nil {
		task.Description = *params.Description
	}
	if params.Status != nil {
		task.Status = *params.Status
	}
	if params.Priority != nil {
		task.Priority = *params.Priority
	}
	if params.PriorityLevel != nil {
		task.PriorityLevel = *params.PriorityLevel
	}
	if params.Assignee != nil {
		task.Assignee = *params.Assignee
	}
	if params.Tags != nil {
		task.Tags = *params.Tags
	}
	if err := s.store.UpdateTaskItem(task); err != nil {
		return nil, fmt.Errorf("failed to update task: %w", err)
	}
	return json.Marshal(map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": fmt.Sprintf("Updated task **%s** — Status: %s, Priority: %s (level %d)", task.Title, task.Status, task.Priority, task.PriorityLevel)},
		},
		"isError": false,
	})
}

func (s *Server) handleDelete(args json.RawMessage) (json.RawMessage, error) {
	var params struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &params)
	if params.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	if err := s.store.DeleteTaskItem(params.ID); err != nil {
		return nil, fmt.Errorf("failed to delete task: %w", err)
	}
	return json.Marshal(map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": fmt.Sprintf("Deleted task %s", params.ID)},
		},
		"isError": false,
	})
}

func (s *Server) handleSearch(args json.RawMessage) (json.RawMessage, error) {
	var params struct {
		Query string `json:"query"`
	}
	json.Unmarshal(args, &params)
	if params.Query == "" {
		return nil, fmt.Errorf("query is required")
	}
	tasks, err := s.store.SearchTaskItems(params.Query)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d matching task(s):\n\n", len(tasks)))
	for _, t := range tasks {
		sb.WriteString(fmt.Sprintf("- **%s** [%s] (%s) — ID: %s\n", t.Title, t.Status, t.Priority, t.ID))
	}
	return json.Marshal(map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": sb.String()},
		},
		"isError": false,
	})
}

func mustJSON(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
