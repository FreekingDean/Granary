package extension

import (
	"errors"
	"fmt"
)

var (
	// ErrNoLayer means a stack that must have a layer is empty.
	ErrNoLayer = errors.New("extension: stack has no layer")
	// ErrAmbiguous means a stack that must have exactly one layer has more.
	ErrAmbiguous = errors.New("extension: stack has more than one layer")
)

// Stack is the ordered layers of an extension the server uses for a call, top
// first. The zero value is an empty stack.
type Stack[T any] struct {
	name   string
	layers []Layer[T]
}

// NewStack builds a stack from layers already in hand. name is the
// extension's name.
func NewStack[T any](name string, layers ...Layer[T]) Stack[T] {
	return Stack[T]{name: name, layers: append([]Layer[T](nil), layers...)}
}

// Len returns the number of layers.
func (s Stack[T]) Len() int { return len(s.layers) }

// Layers returns a copy of the layers, top first.
func (s Stack[T]) Layers() []Layer[T] { return append([]Layer[T](nil), s.layers...) }

// One returns the only layer of a stack (mode None). It is for extensions
// where layering has no meaning.
func One[T any](s Stack[T]) (Layer[T], error) {
	switch len(s.layers) {
	case 0:
		return Layer[T]{}, fmt.Errorf("extension %q: %w", s.name, ErrNoLayer)
	case 1:
		return s.layers[0], nil
	default:
		return Layer[T]{}, fmt.Errorf("extension %q: %w", s.name, ErrAmbiguous)
	}
}

// Route returns the first layer the rule picks (mode Route), and false when
// no layer matches.
func Route[T any](s Stack[T], pick func(Layer[T]) bool) (Layer[T], bool) {
	for _, layer := range s.layers {
		if pick(layer) {
			return layer, true
		}
	}
	return Layer[T]{}, false
}
