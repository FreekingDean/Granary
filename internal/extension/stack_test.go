package extension

import (
	"errors"
	"testing"
)

func stackOf(layerIDs ...string) Stack[string] {
	layers := make([]Layer[string], 0, len(layerIDs))
	for _, id := range layerIDs {
		layers = append(layers, Layer[string]{ID: id, Impl: id})
	}
	return NewStack("test", layers...)
}

func TestNewStack(t *testing.T) {
	tests := []struct {
		name   string
		layers []string
	}{
		{name: "empty"},
		{name: "keeps order", layers: []string{"b", "a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stack := stackOf(tt.layers...)
			if stack.Len() != len(tt.layers) {
				t.Fatalf("Len() = %d, want %d", stack.Len(), len(tt.layers))
			}
			for i, layer := range stack.Layers() {
				if layer.ID != tt.layers[i] {
					t.Fatalf("layer %d = %q, want %q", i, layer.ID, tt.layers[i])
				}
			}
		})
	}
}

func TestStackLayersReturnsACopy(t *testing.T) {
	stack := stackOf("a", "b")
	stack.Layers()[0].ID = "changed"
	if got := stack.Layers()[0].ID; got != "a" {
		t.Fatalf("stack's first layer = %q after editing the returned slice, want a", got)
	}
}

func TestOne(t *testing.T) {
	tests := []struct {
		name    string
		layers  []string
		want    string
		wantErr error
	}{
		{name: "empty", wantErr: ErrNoLayer},
		{name: "single", layers: []string{"only"}, want: "only"},
		{name: "several", layers: []string{"a", "b"}, wantErr: ErrAmbiguous},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := One(stackOf(tt.layers...))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got.ID != tt.want {
				t.Fatalf("layer = %q, want %q", got.ID, tt.want)
			}
		})
	}
}

func TestRoute(t *testing.T) {
	tests := []struct {
		name   string
		layers []string
		pick   string
		want   int // index of the expected layer, -1 for none
	}{
		{name: "empty stack", pick: "a", want: -1},
		{name: "no match", layers: []string{"a", "b"}, pick: "c", want: -1},
		{name: "match", layers: []string{"a", "b"}, pick: "b", want: 1},
		{name: "first match wins", layers: []string{"a", "b", "b"}, pick: "b", want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seen := -1
			got, ok := Route(stackOf(tt.layers...), func(layer Layer[string]) bool {
				seen++
				return layer.ID == tt.pick
			})
			if ok != (tt.want >= 0) {
				t.Fatalf("ok = %v, want %v", ok, tt.want >= 0)
			}
			if ok && (got.ID != tt.pick || seen != tt.want) {
				t.Fatalf("picked %q at index %d, want %q at index %d", got.ID, seen, tt.pick, tt.want)
			}
		})
	}
}
