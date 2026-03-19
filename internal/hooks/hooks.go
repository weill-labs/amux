package hooks

import (
	"fmt"
	"io"
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

// AllEvents lists all valid hook events in display order.
var AllEvents = []Event{OnIdle, OnActivity}

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

// Result is the outcome of a hook command execution.
type Result struct {
	Event   Event
	Command string
	Err     error
}

// Registry stores hooks and executes them on events.
type Registry struct {
	mu          sync.RWMutex
	hooks       map[Event][]Entry
	ErrorWriter io.Writer // if set, errors are written here instead of os.Stderr
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

// Fire executes all hooks for an event asynchronously.
// Each hook command runs as a shell command with the given environment variables.
func (r *Registry) Fire(event Event, env map[string]string) {
	r.FireWithCallback(event, env, nil)
}

// FireWithCallback executes all hooks for an event asynchronously and invokes
// onResult after each command completes.
func (r *Registry) FireWithCallback(event Event, env map[string]string, onResult func(Result)) {
	w := r.ErrorWriter
	if w == nil {
		w = os.Stderr
	}
	for _, entry := range r.List(event) {
		go func(entry Entry) {
			err := executeHook(entry.Command, env, w)
			if onResult != nil {
				onResult(Result{Event: event, Command: entry.Command, Err: err})
			}
		}(entry)
	}
}

// executeHook runs a shell command with additional environment variables.
// Errors are logged to stderr (the server's stderr goes to the session log).
func executeHook(command string, env map[string]string, w io.Writer) error {
	cmd := exec.Command("sh", "-c", command)
	cmd.Env = append(os.Environ(), mapToEnv(env)...)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(w, "hook %q failed: %v\n", command, err)
		return err
	}
	return nil
}

// mapToEnv converts a map to KEY=VALUE slice.
func mapToEnv(m map[string]string) []string {
	result := make([]string, 0, len(m))
	for k, v := range m {
		result = append(result, k+"="+v)
	}
	return result
}
