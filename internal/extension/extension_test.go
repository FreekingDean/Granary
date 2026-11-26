package extension

import (
	"reflect"
	"testing"
)

func ids[T any](layers []Layer[T]) []string {
	out := make([]string, 0, len(layers))
	for _, layer := range layers {
		out = append(out, layer.ID)
	}
	return out
}

func TestNew(t *testing.T) {
	tests := []struct {
		name      string
		extName   string
		wantPanic bool
	}{
		{name: "named", extName: "metadata"},
		{name: "empty name panics", extName: "", wantPanic: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if got := recover() != nil; got != tt.wantPanic {
					t.Fatalf("panicked = %v, want %v", got, tt.wantPanic)
				}
			}()
			if got := New[string](tt.extName).Name(); got != tt.extName {
				t.Fatalf("Name() = %q, want %q", got, tt.extName)
			}
		})
	}
}

func TestExtensionRegister(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		wantPanic bool
	}{
		{name: "new id", id: "b"},
		{name: "existing id is replaced", id: "a"},
		{name: "empty id panics", id: "", wantPanic: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ext := New[string]("test")
			ext.Register("a", "old")
			defer func() {
				if got := recover() != nil; got != tt.wantPanic {
					t.Fatalf("panicked = %v, want %v", got, tt.wantPanic)
				}
			}()
			ext.Register(tt.id, "new")
			if got := ext.Stack(tt.id).Layers(); len(got) != 1 || got[0].Impl != "new" {
				t.Fatalf("layer %q = %+v, want the new registration", tt.id, got)
			}
		})
	}
}

func TestExtensionLayers(t *testing.T) {
	tests := []struct {
		name     string
		register []string
		want     []string
	}{
		{name: "none", want: []string{}},
		{name: "sorted by id", register: []string{"tmdb", "nfo", "arr"}, want: []string{"arr", "nfo", "tmdb"}},
		{name: "one per id", register: []string{"nfo", "nfo"}, want: []string{"nfo"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ext := New[string]("test")
			for _, id := range tt.register {
				ext.Register(id, id)
			}
			if got := ids(ext.Layers()); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Layers() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtensionStack(t *testing.T) {
	tests := []struct {
		name string
		ids  []string
		want []string
	}{
		{name: "no ids", want: []string{}},
		{name: "keeps the requested order", ids: []string{"c", "a", "b"}, want: []string{"c", "a", "b"}},
		{name: "subset", ids: []string{"b"}, want: []string{"b"}},
		{name: "unregistered id is left out", ids: []string{"a", "missing", "c"}, want: []string{"a", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ext := New[string]("test")
			for _, id := range []string{"a", "b", "c"} {
				ext.Register(id, id)
			}
			if got := ids(ext.Stack(tt.ids...).Layers()); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Stack(%v) = %v, want %v", tt.ids, got, tt.want)
			}
		})
	}
}
