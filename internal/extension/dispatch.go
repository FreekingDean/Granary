package extension

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrPass is returned by a layer that declines a call it was offered. In
	// Priority mode the next layer is asked; in All mode the layer simply
	// contributes nothing.
	ErrPass = errors.New("extension: layer passed")
	// ErrNoLayer means a stack that must have a layer is empty.
	ErrNoLayer = errors.New("extension: stack has no layer")
	// ErrAmbiguous means a stack that must have exactly one layer has more.
	ErrAmbiguous = errors.New("extension: stack has more than one layer")
	// ErrNoAnswer means every layer of a Priority call passed or failed.
	ErrNoAnswer = errors.New("extension: no layer answered")
	// ErrNoRoute means no layer matched a Route rule.
	ErrNoRoute = errors.New("extension: no layer matches the route")
)

// Outcome is how one layer call ended.
type Outcome string

const (
	OutcomeAnswered Outcome = "answered"
	OutcomePassed   Outcome = "passed"
	OutcomeFailed   Outcome = "failed"
)

// Event describes one layer call, for logs and metrics.
type Event struct {
	Point    string
	Scope    Scope
	Plugin   PluginRef
	Mode     string // "priority" or "all"
	Outcome  Outcome
	Err      error
	Duration time.Duration
}

// Call identifies the layer a dispatched call is being made on. Dispatch puts
// it in the context it hands to the layer call.
type Call struct {
	Point  string
	Scope  Scope
	Plugin PluginRef
}

type callKey struct{}

// CallFrom returns the layer call a context belongs to.
func CallFrom(ctx context.Context) (Call, bool) {
	call, ok := ctx.Value(callKey{}).(Call)
	return call, ok
}

// One returns the only layer of a stack (mode None). It is for points where
// layering has no meaning.
func One[T any](s Stack[T]) (Layer[T], error) {
	switch len(s.layers) {
	case 0:
		return Layer[T]{}, fmt.Errorf("point %q: %w", s.point, ErrNoLayer)
	case 1:
		return s.layers[0], nil
	default:
		return Layer[T]{}, fmt.Errorf("point %q: %w", s.point, ErrAmbiguous)
	}
}

// Route returns the first layer the rule picks (mode Route).
func Route[T any](s Stack[T], pick func(Layer[T]) bool) (Layer[T], error) {
	for _, layer := range s.layers {
		if pick(layer) {
			return layer, nil
		}
	}
	return Layer[T]{}, fmt.Errorf("point %q: %w", s.point, ErrNoRoute)
}

// Priority asks each layer in turn, top first, and returns the first answer
// with the plugin that gave it (mode Priority). A layer that returns ErrPass,
// fails, or exceeds the point's timeout is skipped and the next one is asked.
// When no layer answers, the error wraps ErrNoAnswer and every layer failure.
// When the caller's own context ends, Priority stops and returns its error.
func Priority[T, R any](ctx context.Context, s Stack[T], call func(context.Context, T) (R, error)) (R, PluginRef, error) {
	var zero R
	if len(s.layers) == 0 {
		return zero, PluginRef{}, fmt.Errorf("point %q: %w", s.point, ErrNoLayer)
	}
	var failures []error
	for _, layer := range s.layers {
		if err := ctx.Err(); err != nil {
			return zero, PluginRef{}, err
		}
		value, err := invoke(ctx, s, layer, "priority", call)
		switch {
		case err == nil:
			return value, layer.Plugin, nil
		case errors.Is(err, ErrPass):
		default:
			failures = append(failures, fmt.Errorf("%s: %w", layer.Plugin, err))
		}
	}
	if err := ctx.Err(); err != nil {
		return zero, PluginRef{}, err
	}
	return zero, PluginRef{}, fmt.Errorf("point %q: %w", s.point, errors.Join(append([]error{ErrNoAnswer}, failures...)...))
}

// Result is one layer's contribution to an All call.
type Result[R any] struct {
	Plugin PluginRef
	Value  R
	// Err is nil for an answer, ErrPass for a layer that declined, and the
	// failure otherwise.
	Err error
}

// Answered reports whether the layer contributed a value.
func (r Result[R]) Answered() bool { return r.Err == nil }

// All calls every layer, top first, and returns one result per layer in stack
// order (mode All). A layer failing or timing out does not stop the others.
// Combining the results belongs to the caller: the point's contract says how.
// When the caller's own context ends, the remaining layers are not called and
// carry its error.
func All[T, R any](ctx context.Context, s Stack[T], call func(context.Context, T) (R, error)) []Result[R] {
	results := make([]Result[R], 0, len(s.layers))
	for _, layer := range s.layers {
		if err := ctx.Err(); err != nil {
			results = append(results, Result[R]{Plugin: layer.Plugin, Err: err})
			continue
		}
		value, err := invoke(ctx, s, layer, "all", call)
		if err != nil {
			var zero R
			value = zero
		}
		results = append(results, Result[R]{Plugin: layer.Plugin, Value: value, Err: err})
	}
	return results
}

func invoke[T, R any](ctx context.Context, s Stack[T], layer Layer[T], mode string, call func(context.Context, T) (R, error)) (R, error) {
	callCtx := context.WithValue(ctx, callKey{}, Call{Point: s.point, Scope: s.scope, Plugin: layer.Plugin})
	if s.timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(callCtx, s.timeout)
		defer cancel()
	}
	start := time.Now()
	value, err := call(callCtx, layer.Impl)
	if s.observe != nil {
		event := Event{Point: s.point, Scope: s.scope, Plugin: layer.Plugin, Mode: mode, Duration: time.Since(start)}
		switch {
		case err == nil:
			event.Outcome = OutcomeAnswered
		case errors.Is(err, ErrPass):
			event.Outcome = OutcomePassed
		default:
			event.Outcome = OutcomeFailed
			event.Err = err
		}
		s.observe(event)
	}
	return value, err
}
