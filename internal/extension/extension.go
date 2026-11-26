// Package extension is the plugin host's dispatch layer. It has three types:
//
//   - A Layer is one implementation of a contract.
//   - An Extension is a named contract and every layer available for it.
//   - A Stack is the ordered layers the server uses for a call, with the
//     helpers that choose among them.
//
// Starting and supervising plugin processes stays in pluginhost. See
// docs/architecture/plugin-host.md.
package extension

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
)

// Layer is one implementation of an extension's contract.
type Layer[T any] struct {
	ID   string
	Impl T
}

// Extension is a named contract T and every layer registered for it. Declare
// one as a package-level variable in the package that owns the behavior.
type Extension[T any] struct {
	name string

	mu     sync.RWMutex
	layers map[string]T
}

// New declares an extension. The name appears in logs and errors.
func New[T any](name string) *Extension[T] {
	if name == "" {
		panic("extension: New requires a name")
	}
	return &Extension[T]{name: name, layers: map[string]T{}}
}

// Name returns the extension's name.
func (e *Extension[T]) Name() string { return e.name }

// Register makes a layer available under id. A later registration for the
// same id replaces the earlier one. It panics on an empty id, since it is
// called from init and a silent no-op would disable a layer.
func (e *Extension[T]) Register(id string, impl T) {
	if id == "" {
		panic(fmt.Sprintf("extension: %q: Register requires an id", e.name))
	}
	e.mu.Lock()
	e.layers[id] = impl
	e.mu.Unlock()
}

// Layers returns every registered layer, sorted by id.
func (e *Extension[T]) Layers() []Layer[T] {
	e.mu.RLock()
	layers := make([]Layer[T], 0, len(e.layers))
	for id, impl := range e.layers {
		layers = append(layers, Layer[T]{ID: id, Impl: impl})
	}
	e.mu.RUnlock()
	sort.Slice(layers, func(i, j int) bool { return layers[i].ID < layers[j].ID })
	return layers
}

// Stack returns the layers named by ids, in that order, top first. An id with
// no registered layer is logged and left out, so one missing plugin does not
// take the others down with it.
func (e *Extension[T]) Stack(ids ...string) Stack[T] {
	layers := make([]Layer[T], 0, len(ids))
	e.mu.RLock()
	for _, id := range ids {
		impl, ok := e.layers[id]
		if !ok {
			slog.Warn("skipping unregistered extension layer", "component", "extension", "extension", e.name, "layer", id)
			continue
		}
		layers = append(layers, Layer[T]{ID: id, Impl: impl})
	}
	e.mu.RUnlock()
	return Stack[T]{name: e.name, layers: layers}
}
