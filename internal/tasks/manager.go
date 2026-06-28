package tasks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

// Task represents a tracked task with its result and metadata.
type Task struct {
	TaskID        string
	Status        string // "working", "input_required", "completed", "failed", "cancelled"
	StatusMessage string
	CreatedAt     time.Time
	LastUpdatedAt time.Time
	TTL           *time.Duration // nil = unlimited
	PollInterval  *time.Duration

	// Auth binding: the API key ID or OIDC subject that created this task.
	// Tasks are only accessible to the same auth context.
	AuthKeyID string

	// Result of the task (set when terminal)
	Result json.RawMessage // raw JSON-RPC result
	Err    error           // set when task failed

	// done channel is closed when the task reaches a terminal state.
	// tasks/result blocks on this until terminal.
	done chan struct{}
}

// IsTerminal returns true if the task is in a terminal state.
func (t *Task) IsTerminal() bool {
	return t.Status == "completed" || t.Status == "failed" || t.Status == "cancelled"
}

// Manager manages tasks in memory with TTL-based expiry.
// Tasks are bound to an auth context (API key ID) for security.
type Manager struct {
	mu    sync.RWMutex
	tasks map[string]*Task // taskID -> task
}

// New creates a new task Manager.
func New() *Manager {
	m := &Manager{
		tasks: make(map[string]*Task),
	}
	// Start cleanup goroutine for expired tasks
	go m.cleanupLoop()
	return m
}

// CreateTask creates a new task in "working" status and returns it.
// The authKeyID binds the task to the caller's auth context.
func (m *Manager) CreateTask(authKeyID string, ttlMS int64) *Task {
	taskID := generateTaskID()
	now := time.Now()

	var ttl *time.Duration
	if ttlMS > 0 {
		d := time.Duration(ttlMS) * time.Millisecond
		ttl = &d
	}

	t := &Task{
		TaskID:        taskID,
		Status:        "working",
		StatusMessage: "The operation is now in progress.",
		CreatedAt:     now,
		LastUpdatedAt: now,
		TTL:           ttl,
		AuthKeyID:     authKeyID,
		done:          make(chan struct{}),
	}

	m.mu.Lock()
	m.tasks[taskID] = t
	m.mu.Unlock()

	return t
}

// Get retrieves a task by ID. Returns nil if not found.
// If authKeyID is non-empty, the task's AuthKeyID must match.
func (m *Manager) Get(taskID, authKeyID string) (*Task, error) {
	m.mu.RLock()
	t, ok := m.tasks[taskID]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	if authKeyID != "" && t.AuthKeyID != authKeyID {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	return t, nil
}

// CompleteTask marks a task as completed and stores the result.
func (m *Manager) CompleteTask(taskID string, result json.RawMessage) {
	m.mu.Lock()
	t, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return
	}
	if t.IsTerminal() {
		m.mu.Unlock()
		return
	}
	t.Status = "completed"
	t.LastUpdatedAt = time.Now()
	t.Result = result
	t.Err = nil
	close(t.done)
	m.mu.Unlock()
}

// FailTask marks a task as failed.
func (m *Manager) FailTask(taskID string, err error) {
	m.mu.Lock()
	t, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return
	}
	if t.IsTerminal() {
		m.mu.Unlock()
		return
	}
	log.Printf("[Tasks] Task %s failed: %v", taskID, err)
	t.Status = "failed"
	t.StatusMessage = "The operation failed."
	t.LastUpdatedAt = time.Now()
	t.Err = err
	close(t.done)
	m.mu.Unlock()
}

// CancelTask transitions a task to cancelled status.
// Returns error if the task doesn't exist or is already terminal.
func (m *Manager) CancelTask(taskID, authKeyID string) (*Task, error) {
	m.mu.Lock()
	t, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	if authKeyID != "" && t.AuthKeyID != authKeyID {
		m.mu.Unlock()
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	if t.IsTerminal() {
		m.mu.Unlock()
		return nil, fmt.Errorf("cannot cancel task in terminal status: %s", t.Status)
	}
	t.Status = "cancelled"
	t.LastUpdatedAt = time.Now()
	close(t.done)
	m.mu.Unlock()
	return t, nil
}

// ListTasks returns tasks for the given authKeyID, with pagination.
func (m *Manager) ListTasks(authKeyID string, cursor string, limit int) ([]*Task, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var all []*Task
	for _, t := range m.tasks {
		if authKeyID != "" && t.AuthKeyID != authKeyID {
			continue
		}
		all = append(all, t)
	}

	// Simple cursor: skip until we find the cursor taskID, then take limit
	start := 0
	if cursor != "" {
		for i, t := range all {
			if t.TaskID == cursor {
				start = i + 1
				break
			}
		}
	}

	var result []*Task
	nextCursor := ""
	for i := start; i < len(all); i++ {
		if len(result) >= limit {
			nextCursor = all[i].TaskID
			break
		}
		result = append(result, all[i])
	}

	return result, nextCursor, nil
}

// WaitForResult blocks until the task reaches a terminal state or ctx is done.
// Returns the task's result and error.
func (m *Manager) WaitForResult(ctx context.Context, taskID, authKeyID string) (json.RawMessage, error) {
	t, err := m.Get(taskID, authKeyID)
	if err != nil {
		return nil, err
	}

	// If already terminal, return immediately
	if t.IsTerminal() {
		return t.Result, t.Err
	}

	// Wait for completion
	select {
	case <-t.done:
		return t.Result, t.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Delete removes a task from the manager.
func (m *Manager) Delete(taskID string) {
	m.mu.Lock()
	delete(m.tasks, taskID)
	m.mu.Unlock()
}

// cleanupLoop periodically removes expired tasks.
func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		m.cleanupExpired()
	}
}

func (m *Manager) cleanupExpired() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for id, t := range m.tasks {
		// Clean up tasks whose TTL has expired
		if t.TTL != nil && now.Sub(t.CreatedAt) > *t.TTL {
			delete(m.tasks, id)
			log.Printf("[Tasks] Cleaned up expired task %s", id)
			continue
		}
		// Clean up terminal tasks older than maxTaskLifetime (1 hour),
		// even if they have no TTL or a longer TTL. This prevents
		// unbounded memory growth from abandoned tasks.
		if t.IsTerminal() && now.Sub(t.CreatedAt) > maxTaskLifetime {
			delete(m.tasks, id)
			log.Printf("[Tasks] Cleaned up old terminal task %s (age=%v)", id, now.Sub(t.CreatedAt).Round(time.Second))
		}
	}
}

// maxTaskLifetime is the maximum time any task can exist before cleanup,
// regardless of TTL. Terminal tasks are cleaned up after this duration
// to prevent unbounded memory growth from abandoned tasks.
const maxTaskLifetime = 1 * time.Hour

// generateTaskID creates a cryptographically secure, globally unique task ID.
// Per spec: task IDs MUST be generated by the receiver, MUST be unique, and
// MUST have enough entropy to prevent guessing (when auth binding is used).
func generateTaskID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID — ensures uniqueness even if crypto/rand fails
		log.Printf("WARNING: crypto/rand failed in generateTaskID: %v — using fallback", err)
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
