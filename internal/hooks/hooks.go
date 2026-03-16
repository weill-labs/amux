package hooks

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
)

// Event is a hook event type.
type Event string

const (
	OnIdle     Event = "on-idle"
	OnActivity Event = "on-activity"
)

// ParseEvent validates and returns a hook event from a string.
func ParseEvent(s string) (Event, error) {
	switch Event(s) {
	case OnIdle:
		return OnIdle, nil
	case OnActivity:
		return OnActivity, nil
	default:
		return "", fmt.Errorf("unknown hook event: %q (valid: on-idle, on-activity)", s)
	}
}

// Entry is a registered hook: an event paired with a shell command.
type Entry struct {
	Event   Event
	Command string
}

// Registry stores hooks and executes them on events.
type Registry struct {
	mu    sync.RWMutex
	hooks map[Event][]Entry
}

// NewRegistry creates an empty hook registry.
func NewRegistry() *Registry {
	return &Registry{
		hooks: make(map[Event][]Entry),
	}
}

// Add registers a hook command for an event.
func (r *Registry) Add(event Event, command string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks[event] = append(r.hooks[event], Entry{Event: event, Command: command})
}

// Remove removes a hook by event and 0-based index.
func (r *Registry) Remove(event Event, index int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entries := r.hooks[event]
	if index < 0 || index >= len(entries) {
		return
	}
	r.hooks[event] = append(entries[:index], entries[index+1:]...)
}

// RemoveAll removes all hooks for an event.
func (r *Registry) RemoveAll(event Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.hooks, event)
}

// List returns all hooks for an event.
func (r *Registry) List(event Event) []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries := r.hooks[event]
	result := make([]Entry, len(entries))
	copy(result, entries)
	return result
}

// ListAll returns all registered hooks across all events.
func (r *Registry) ListAll() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []Entry
	for _, entries := range r.hooks {
		result = append(result, entries...)
	}
	return result
}

// Fire executes all hooks for an event asynchronously.
// Each hook command runs as a shell command with the given environment variables.
func (r *Registry) Fire(event Event, env map[string]string) {
	r.mu.RLock()
	entries := make([]Entry, len(r.hooks[event]))
	copy(entries, r.hooks[event])
	r.mu.RUnlock()

	for _, entry := range entries {
		go executeHook(entry.Command, env)
	}
}

// executeHook runs a shell command with additional environment variables.
// Errors are logged to stderr (the server's stderr goes to the session log).
func executeHook(command string, env map[string]string) {
	cmd := exec.Command("sh", "-c", command)
	cmd.Env = append(os.Environ(), mapToEnv(env)...)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "hook %q failed: %v\n", command, err)
	}
}

// mapToEnv converts a map to KEY=VALUE slice.
func mapToEnv(m map[string]string) []string {
	result := make([]string, 0, len(m))
	for k, v := range m {
		result = append(result, k+"="+v)
	}
	return result
}
