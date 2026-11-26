// Package extension is the plugin host's dispatch layer. A feature declares an
// extension point for a Go interface, registers the bundled implementations
// compiled into the server, and asks for the stack of layers configured for a
// scope. The dispatch helpers in dispatch.go then call that stack in one of
// four modes. Starting and supervising plugin processes stays in pluginhost;
// this package only decides which implementations are called and in what
// order. See docs/architecture/plugin-host.md.
package extension

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// PluginRef names one capability of one installed plugin.
type PluginRef struct {
	InstallationID int
	PluginID       string
	CapabilityID   string
}

func (r PluginRef) String() string {
	if r.PluginID == "" {
		return fmt.Sprintf("installation %d/%s", r.InstallationID, r.CapabilityID)
	}
	return fmt.Sprintf("%s/%s", r.PluginID, r.CapabilityID)
}

// Scope selects which stack of a point is wanted. Some points have one global
// stack; others, like metadata, have one per library and content level.
type Scope struct {
	Kind string // "global", "library"
	ID   string
	Sub  string // optional refinement, such as a content level
}

// Global is the scope of a point that has a single stack.
var Global = Scope{Kind: "global"}

// Entry is one configured layer of a stack, before it is resolved to an
// implementation.
type Entry struct {
	Plugin PluginRef
	// Bundled marks an implementation compiled into the server. It is looked
	// up by Plugin.CapabilityID in the point's registry; any other entry is
	// resolved through the binding's remote adapter.
	Bundled bool
}

// StackSource reports the layers configured for a scope, top of the stack
// first. Each point supplies its own, so a point keeps whatever storage it
// already has. Entries for disabled plugins are left out by the source.
type StackSource interface {
	Entries(ctx context.Context, scope Scope) ([]Entry, error)
}

// StackSourceFunc adapts a function to StackSource.
type StackSourceFunc func(ctx context.Context, scope Scope) ([]Entry, error)

func (f StackSourceFunc) Entries(ctx context.Context, scope Scope) ([]Entry, error) {
	return f(ctx, scope)
}

// RemoteFunc wraps an out-of-process plugin capability as a T.
type RemoteFunc[T any] func(ctx context.Context, plugin PluginRef) (T, error)

// Point is an extension point: a named contract T and the registry of its
// bundled implementations. Declare one as a package-level variable in the
// package that owns the behavior.
type Point[T any] struct {
	name    string
	timeout time.Duration
	observe func(Event)

	mu      sync.RWMutex
	bundled map[string]func() T
}

// Option configures a Point.
type Option func(*pointOptions)

type pointOptions struct {
	timeout time.Duration
	observe func(Event)
}

// WithTimeout bounds each layer call made through Priority or All. Zero, the
// default, leaves the caller's deadline as the only bound.
func WithTimeout(d time.Duration) Option {
	return func(o *pointOptions) { o.timeout = d }
}

// WithObserver is called once after every layer call made through Priority or
// All. It must not block.
func WithObserver(observe func(Event)) Option {
	return func(o *pointOptions) { o.observe = observe }
}

// NewPoint declares an extension point. The name appears in logs and events.
func NewPoint[T any](name string, opts ...Option) *Point[T] {
	if name == "" {
		panic("extension: NewPoint requires a name")
	}
	var o pointOptions
	for _, opt := range opts {
		opt(&o)
	}
	return &Point[T]{name: name, timeout: o.timeout, observe: o.observe, bundled: map[string]func() T{}}
}

// Name returns the point's name.
func (p *Point[T]) Name() string { return p.name }

// Bundled registers the constructor of an implementation compiled into the
// server, under its capability id. A later registration for the same id
// replaces the earlier one. It is meant to be called from package init, so an
// empty id or nil constructor panics rather than silently disabling a layer.
func (p *Point[T]) Bundled(capabilityID string, construct func() T) {
	if capabilityID == "" || construct == nil {
		panic(fmt.Sprintf("extension: point %q: Bundled requires a capability id and a constructor", p.name))
	}
	p.mu.Lock()
	p.bundled[capabilityID] = construct
	p.mu.Unlock()
}

// BundledIDs returns the registered bundled capability ids, sorted.
func (p *Point[T]) BundledIDs() []string {
	p.mu.RLock()
	ids := make([]string, 0, len(p.bundled))
	for id := range p.bundled {
		ids = append(ids, id)
	}
	p.mu.RUnlock()
	sort.Strings(ids)
	return ids
}

// NewBundled constructs the bundled implementation registered under
// capabilityID.
func (p *Point[T]) NewBundled(capabilityID string) (T, bool) {
	p.mu.RLock()
	construct, ok := p.bundled[capabilityID]
	p.mu.RUnlock()
	if !ok {
		var zero T
		return zero, false
	}
	return construct(), true
}

// Bind attaches the runtime dependencies a point needs to resolve stacks:
// where its configured layers are stored, and how to reach an out-of-process
// plugin. remote may be nil for a point that only has bundled layers.
func (p *Point[T]) Bind(source StackSource, remote RemoteFunc[T]) *Binding[T] {
	if source == nil {
		panic(fmt.Sprintf("extension: point %q: Bind requires a stack source", p.name))
	}
	return &Binding[T]{point: p, source: source, remote: remote}
}

// Binding resolves stacks for a point.
type Binding[T any] struct {
	point  *Point[T]
	source StackSource
	remote RemoteFunc[T]
}

// Stack resolves the layers configured for scope, top first. An entry that
// cannot be resolved (no bundled constructor, no remote adapter, or a plugin
// that will not start) is logged and left out, so one broken plugin does not
// take the others down with it. It fails only when the source does.
func (b *Binding[T]) Stack(ctx context.Context, scope Scope) (Stack[T], error) {
	entries, err := b.source.Entries(ctx, scope)
	if err != nil {
		return Stack[T]{}, fmt.Errorf("extension: point %q: list stack entries: %w", b.point.name, err)
	}
	layers := make([]Layer[T], 0, len(entries))
	for _, entry := range entries {
		impl, err := b.resolve(ctx, entry)
		if err != nil {
			slog.WarnContext(ctx, "skipping extension layer", "component", "extension",
				"point", b.point.name, "plugin", entry.Plugin.String(), "bundled", entry.Bundled, "error", err)
			continue
		}
		layers = append(layers, Layer[T]{Plugin: entry.Plugin, Bundled: entry.Bundled, Impl: impl})
	}
	return Stack[T]{point: b.point.name, scope: scope, timeout: b.point.timeout, observe: b.point.observe, layers: layers}, nil
}

func (b *Binding[T]) resolve(ctx context.Context, entry Entry) (T, error) {
	var zero T
	if entry.Bundled {
		impl, ok := b.point.NewBundled(entry.Plugin.CapabilityID)
		if !ok {
			return zero, fmt.Errorf("no bundled implementation registered as %q", entry.Plugin.CapabilityID)
		}
		return impl, nil
	}
	if b.remote == nil {
		return zero, errors.New("point has no remote adapter")
	}
	return b.remote(ctx, entry.Plugin)
}

// Layer is one plugin's implementation at one position of a stack.
type Layer[T any] struct {
	Plugin  PluginRef
	Bundled bool
	Impl    T
}

// Stack is the ordered layers of a point for one scope, top first. The zero
// value is an empty stack.
type Stack[T any] struct {
	point   string
	scope   Scope
	timeout time.Duration
	observe func(Event)
	layers  []Layer[T]
}

// NewStack builds a stack from layers already in hand. Production code gets
// stacks from Binding.Stack; this is for tests and for callers that assemble
// layers themselves.
func NewStack[T any](point string, layers ...Layer[T]) Stack[T] {
	return Stack[T]{point: point, layers: append([]Layer[T](nil), layers...)}
}

// Len returns the number of layers.
func (s Stack[T]) Len() int { return len(s.layers) }

// Layers returns a copy of the layers, top first.
func (s Stack[T]) Layers() []Layer[T] { return append([]Layer[T](nil), s.layers...) }

// Impls returns the implementations in stack order, for a caller that still
// walks the list itself.
func (s Stack[T]) Impls() []T {
	impls := make([]T, 0, len(s.layers))
	for _, layer := range s.layers {
		impls = append(impls, layer.Impl)
	}
	return impls
}
