package cli

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/spf13/cobra"
)

// Handler is the stable registration boundary for commands owned by T12-T14.
// Registrations are explicit composition inputs; package init side effects are
// intentionally not supported.
type Handler func(context.Context, []string, Output) error

// CobraContext is the boundary between the command registry and the Cobra
// tree. Builders own flag definitions and positional validation; Run remains
// the small application-layer adapter used by tests and composition.
type CobraContext struct {
	Run    Handler
	Output func(*cobra.Command) Output
}

type CobraBuilder func(CobraContext) *cobra.Command

type Command struct {
	Name        string
	Description string
	Run         Handler
	Build       CobraBuilder
}

type Registry struct {
	mu       sync.RWMutex
	commands map[string]Command
}

func NewRegistry() *Registry { return &Registry{commands: make(map[string]Command)} }

func (registry *Registry) Register(command Command) error {
	if registry == nil || command.Run == nil || !validCommandName(command.Name) {
		return &UsageError{Message: "invalid command registration"}
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.commands[command.Name]; exists {
		return &UsageError{Message: "duplicate command registration"}
	}
	registry.commands[command.Name] = command
	return nil
}

func (registry *Registry) Lookup(name string) (Command, bool) {
	if registry == nil {
		return Command{}, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	command, ok := registry.commands[name]
	return command, ok
}

func (registry *Registry) Names() []string {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	names := make([]string, 0, len(registry.commands))
	for name := range registry.commands {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func validCommandName(value string) bool {
	return value != "" && !strings.ContainsAny(value, " \t\r\n\x00")
}

var errCommandUnavailable = errors.New("command unavailable")
