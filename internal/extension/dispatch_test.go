package extension

import (
	"context"
	"errors"
	"testing"
)

var errBoom = errors.New("boom")

// step is one layer's scripted behavior. The layer id is its position.
type step struct {
	value  string
	pass   bool
	err    error
	cancel bool // end the caller's context during this call
}

// script builds a stack of scripted layers and the Func that runs them,
// recording which layers were called.
func script(steps []step, cancel context.CancelFunc) (Stack[step], Func[step, string], *[]string) {
	layers := make([]Layer[step], 0, len(steps))
	for i, s := range steps {
		layers = append(layers, Layer[step]{ID: string(rune('a' + i)), Impl: s})
	}
	called := &[]string{}
	call := func(_ context.Context, s step) (string, bool, error) {
		*called = append(*called, s.value)
		if s.cancel {
			cancel()
		}
		return s.value, !s.pass, s.err
	}
	return NewStack("test", layers...), call, called
}

func TestPriority(t *testing.T) {
	tests := []struct {
		name       string
		steps      []step
		want       Result[string]
		wantErr    error
		wantCalled int
	}{
		{
			name:       "empty stack passes",
			want:       Result[string]{Outcome: Passed},
			wantCalled: 0,
		},
		{
			name:       "top layer answers and lower layers are not asked",
			steps:      []step{{value: "one"}, {value: "two"}},
			want:       Result[string]{Layer: "a", Value: "one", Outcome: Answered},
			wantCalled: 1,
		},
		{
			name:       "a pass falls through",
			steps:      []step{{pass: true}, {value: "two"}},
			want:       Result[string]{Layer: "b", Value: "two", Outcome: Answered},
			wantCalled: 2,
		},
		{
			name:       "a failure falls through",
			steps:      []step{{err: errBoom}, {value: "two"}},
			want:       Result[string]{Layer: "b", Value: "two", Outcome: Answered},
			wantCalled: 2,
		},
		{
			name:       "a zero value is still an answer",
			steps:      []step{{value: ""}, {value: "two"}},
			want:       Result[string]{Layer: "a", Outcome: Answered},
			wantCalled: 1,
		},
		{
			name:       "every layer passes",
			steps:      []step{{pass: true}, {pass: true}},
			want:       Result[string]{Outcome: Passed},
			wantCalled: 2,
		},
		{
			name:       "no answer and a failure",
			steps:      []step{{pass: true}, {err: errBoom}},
			want:       Result[string]{Outcome: Failed},
			wantErr:    errBoom,
			wantCalled: 2,
		},
		{
			name:       "caller's context ends",
			steps:      []step{{pass: true, cancel: true}, {value: "late"}},
			want:       Result[string]{Outcome: Failed},
			wantErr:    context.Canceled,
			wantCalled: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stack, call, called := script(tt.steps, cancel)

			got := Priority(ctx, stack, call)
			if !errors.Is(got.Err, tt.wantErr) {
				t.Fatalf("Err = %v, want %v", got.Err, tt.wantErr)
			}
			got.Err = nil
			if got != tt.want {
				t.Fatalf("result = %+v, want %+v", got, tt.want)
			}
			if len(*called) != tt.wantCalled {
				t.Fatalf("called %d layers, want %d", len(*called), tt.wantCalled)
			}
		})
	}
}

func TestAll(t *testing.T) {
	tests := []struct {
		name       string
		steps      []step
		want       []Result[string]
		wantErrs   []error
		wantCalled int
	}{
		{
			name: "empty stack",
			want: []Result[string]{},
		},
		{
			name:  "every layer is called in stack order",
			steps: []step{{value: "one"}, {value: "two"}},
			want: []Result[string]{
				{Layer: "a", Value: "one", Outcome: Answered},
				{Layer: "b", Value: "two", Outcome: Answered},
			},
			wantErrs:   []error{nil, nil},
			wantCalled: 2,
		},
		{
			name:  "a pass and a failure do not stop the rest",
			steps: []step{{pass: true, value: "ignored"}, {err: errBoom, value: "ignored"}, {value: "three"}},
			want: []Result[string]{
				{Layer: "a", Outcome: Passed},
				{Layer: "b", Outcome: Failed},
				{Layer: "c", Value: "three", Outcome: Answered},
			},
			wantErrs:   []error{nil, errBoom, nil},
			wantCalled: 3,
		},
		{
			name:  "caller's context ends",
			steps: []step{{value: "one", cancel: true}, {value: "late"}},
			want: []Result[string]{
				{Layer: "a", Value: "one", Outcome: Answered},
				{Layer: "b", Outcome: Failed},
			},
			wantErrs:   []error{nil, context.Canceled},
			wantCalled: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stack, call, called := script(tt.steps, cancel)

			got := All(ctx, stack, call)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d results, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if !errors.Is(got[i].Err, tt.wantErrs[i]) {
					t.Fatalf("result %d Err = %v, want %v", i, got[i].Err, tt.wantErrs[i])
				}
				got[i].Err = nil
				if got[i] != tt.want[i] {
					t.Fatalf("result %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
			if len(*called) != tt.wantCalled {
				t.Fatalf("called %d layers, want %d", len(*called), tt.wantCalled)
			}
		})
	}
}
