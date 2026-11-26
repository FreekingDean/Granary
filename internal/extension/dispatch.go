package extension

import (
	"context"
	"errors"
	"fmt"
)

// Outcome is how a layer call, or a whole Priority call, ended.
type Outcome string

const (
	// Answered means the layer returned a value.
	Answered Outcome = "answered"
	// Passed means the layer declined the call. It is not a failure.
	Passed Outcome = "passed"
	// Failed means the layer returned an error.
	Failed Outcome = "failed"
)

// Func makes one call on a layer. It returns ok false to pass: the layer has
// nothing to say about this call, and value is ignored.
type Func[T, R any] func(ctx context.Context, impl T) (value R, ok bool, err error)

// Result is what one layer contributed.
type Result[R any] struct {
	// Layer is the id of the layer the result came from.
	Layer   string
	Value   R
	Outcome Outcome
	// Err is set only when Outcome is Failed.
	Err error
}

// Priority asks each layer in turn, top first, and returns the first answer
// (mode Priority). A layer that passes or fails is skipped and the next one
// is asked.
//
// When no layer answers, the result names no layer. Its outcome is Passed if
// every layer passed, which includes an empty stack, and Failed with the
// joined layer errors if any layer failed. When the caller's context ends,
// Priority stops and returns Failed with the context's error.
func Priority[T, R any](ctx context.Context, s Stack[T], call Func[T, R]) Result[R] {
	var failures []error
	for _, layer := range s.layers {
		if err := ctx.Err(); err != nil {
			return Result[R]{Outcome: Failed, Err: err}
		}
		result := invoke(ctx, layer, call)
		switch result.Outcome {
		case Answered:
			return result
		case Failed:
			failures = append(failures, fmt.Errorf("%s: %w", layer.ID, result.Err))
		case Passed:
		}
	}
	if err := ctx.Err(); err != nil {
		return Result[R]{Outcome: Failed, Err: err}
	}
	if len(failures) > 0 {
		return Result[R]{Outcome: Failed, Err: fmt.Errorf("extension %q: %w", s.name, errors.Join(failures...))}
	}
	return Result[R]{Outcome: Passed}
}

// All calls every layer, top first, and returns one result per layer in stack
// order (mode All). A failing layer does not stop the others. Combining the
// results belongs to the caller: the extension's contract says how. When the
// caller's context ends, the remaining layers are not called and their
// results are Failed with the context's error.
func All[T, R any](ctx context.Context, s Stack[T], call Func[T, R]) []Result[R] {
	results := make([]Result[R], 0, len(s.layers))
	for _, layer := range s.layers {
		if err := ctx.Err(); err != nil {
			results = append(results, Result[R]{Layer: layer.ID, Outcome: Failed, Err: err})
			continue
		}
		results = append(results, invoke(ctx, layer, call))
	}
	return results
}

func invoke[T, R any](ctx context.Context, layer Layer[T], call Func[T, R]) Result[R] {
	value, ok, err := call(ctx, layer.Impl)
	switch {
	case err != nil:
		return Result[R]{Layer: layer.ID, Outcome: Failed, Err: err}
	case !ok:
		return Result[R]{Layer: layer.ID, Outcome: Passed}
	default:
		return Result[R]{Layer: layer.ID, Value: value, Outcome: Answered}
	}
}
