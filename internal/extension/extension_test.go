package extension

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// answerer is the contract the tests dispatch over.
type answerer interface {
	Answer(ctx context.Context) (string, error)
}

type answerFunc func(ctx context.Context) (string, error)

func (f answerFunc) Answer(ctx context.Context) (string, error) { return f(ctx) }

func says(value string) answerer {
	return answerFunc(func(context.Context) (string, error) { return value, nil })
}

func fails(err error) answerer {
	return answerFunc(func(context.Context) (string, error) { return "", err })
}

func layer(id string, impl answerer) Layer[answerer] {
	return Layer[answerer]{Plugin: PluginRef{PluginID: id, CapabilityID: "answer"}, Impl: impl}
}

func ask(ctx context.Context, a answerer) (string, error) { return a.Answer(ctx) }

func TestBindingStackResolvesBundledAndRemoteInOrder(t *testing.T) {
	point := NewPoint[answerer]("test.answer")
	point.Bundled("builtin", func() answerer { return says("builtin") })

	entries := []Entry{
		{Plugin: PluginRef{InstallationID: 7, PluginID: "remote", CapabilityID: "answer"}},
		{Plugin: PluginRef{PluginID: "core", CapabilityID: "missing"}, Bundled: true},
		{Plugin: PluginRef{InstallationID: 8, PluginID: "broken", CapabilityID: "answer"}},
		{Plugin: PluginRef{PluginID: "core", CapabilityID: "builtin"}, Bundled: true},
	}
	var gotScope Scope
	source := StackSourceFunc(func(_ context.Context, scope Scope) ([]Entry, error) {
		gotScope = scope
		return entries, nil
	})
	remote := func(_ context.Context, plugin PluginRef) (answerer, error) {
		if plugin.PluginID == "broken" {
			return nil, errors.New("will not start")
		}
		return says("remote"), nil
	}

	scope := Scope{Kind: "library", ID: "3", Sub: "movie"}
	stack, err := point.Bind(source, remote).Stack(context.Background(), scope)
	if err != nil {
		t.Fatalf("Stack: %v", err)
	}
	if gotScope != scope {
		t.Fatalf("source got scope %+v, want %+v", gotScope, scope)
	}

	// The unregistered bundled entry and the plugin that will not start are
	// left out; the two that resolve keep their configured order.
	var got []string
	for _, r := range All(context.Background(), stack, ask) {
		got = append(got, r.Plugin.PluginID+"="+r.Value)
	}
	if want := []string{"remote=remote", "core=builtin"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved layers = %v, want %v", got, want)
	}
}

func TestBindingStackWithoutRemoteAdapterSkipsRemoteEntries(t *testing.T) {
	point := NewPoint[answerer]("test.answer")
	point.Bundled("builtin", func() answerer { return says("builtin") })
	source := StackSourceFunc(func(context.Context, Scope) ([]Entry, error) {
		return []Entry{
			{Plugin: PluginRef{PluginID: "remote", CapabilityID: "answer"}},
			{Plugin: PluginRef{CapabilityID: "builtin"}, Bundled: true},
		}, nil
	})
	stack, err := point.Bind(source, nil).Stack(context.Background(), Global)
	if err != nil {
		t.Fatalf("Stack: %v", err)
	}
	if stack.Len() != 1 || !stack.Layers()[0].Bundled {
		t.Fatalf("layers = %+v, want only the bundled one", stack.Layers())
	}
}

func TestBindingStackFailsWhenSourceFails(t *testing.T) {
	boom := errors.New("database down")
	point := NewPoint[answerer]("test.answer")
	source := StackSourceFunc(func(context.Context, Scope) ([]Entry, error) { return nil, boom })
	if _, err := point.Bind(source, nil).Stack(context.Background(), Global); !errors.Is(err, boom) {
		t.Fatalf("Stack error = %v, want it to wrap the source error", err)
	}
}

func TestOne(t *testing.T) {
	if _, err := One(NewStack[answerer]("p")); !errors.Is(err, ErrNoLayer) {
		t.Fatalf("empty stack: err = %v, want ErrNoLayer", err)
	}
	got, err := One(NewStack("p", layer("only", says("x"))))
	if err != nil || got.Plugin.PluginID != "only" {
		t.Fatalf("single layer: got %+v, %v", got.Plugin, err)
	}
	if _, err := One(NewStack("p", layer("a", says("x")), layer("b", says("y")))); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("two layers: err = %v, want ErrAmbiguous", err)
	}
}

func TestRoute(t *testing.T) {
	stack := NewStack("p", layer("a", says("x")), layer("b", says("y")), layer("b", says("z")))
	got, err := Route(stack, func(l Layer[answerer]) bool { return l.Plugin.PluginID == "b" })
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if value, _ := got.Impl.Answer(context.Background()); value != "y" {
		t.Fatalf("routed to %q, want the first match", value)
	}
	if _, err := Route(stack, func(Layer[answerer]) bool { return false }); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("no match: err = %v, want ErrNoRoute", err)
	}
}

func TestPriorityFallsThroughPassesAndFailures(t *testing.T) {
	boom := errors.New("boom")
	called := false
	stack := NewStack("p",
		layer("passes", fails(ErrPass)),
		layer("breaks", fails(boom)),
		layer("answers", says("hello")),
		layer("below", answerFunc(func(context.Context) (string, error) {
			called = true
			return "unreachable", nil
		})),
	)
	value, plugin, err := Priority(context.Background(), stack, ask)
	if err != nil {
		t.Fatalf("Priority: %v", err)
	}
	if value != "hello" || plugin.PluginID != "answers" {
		t.Fatalf("got %q from %q, want hello from answers", value, plugin.PluginID)
	}
	if called {
		t.Fatal("a layer below the answer was called")
	}
}

func TestPriorityReportsNoAnswerWithFailures(t *testing.T) {
	boom := errors.New("boom")
	stack := NewStack("p", layer("passes", fails(ErrPass)), layer("breaks", fails(boom)))
	_, _, err := Priority(context.Background(), stack, ask)
	if !errors.Is(err, ErrNoAnswer) || !errors.Is(err, boom) {
		t.Fatalf("err = %v, want ErrNoAnswer joined with the layer failure", err)
	}
	if errors.Is(err, ErrPass) {
		t.Fatalf("err = %v, a pass is not a failure and should not be reported", err)
	}

	if _, _, err := Priority(context.Background(), NewStack[answerer]("p"), ask); !errors.Is(err, ErrNoLayer) {
		t.Fatalf("empty stack: err = %v, want ErrNoLayer", err)
	}
}

func TestPrioritySkipsLayerThatExceedsPointTimeout(t *testing.T) {
	point := NewPoint[answerer]("test.answer", WithTimeout(10*time.Millisecond))
	point.Bundled("slow", func() answerer {
		return answerFunc(func(ctx context.Context) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		})
	})
	point.Bundled("fast", func() answerer { return says("fast") })
	source := StackSourceFunc(func(context.Context, Scope) ([]Entry, error) {
		return []Entry{
			{Plugin: PluginRef{CapabilityID: "slow"}, Bundled: true},
			{Plugin: PluginRef{CapabilityID: "fast"}, Bundled: true},
		}, nil
	})
	stack, err := point.Bind(source, nil).Stack(context.Background(), Global)
	if err != nil {
		t.Fatalf("Stack: %v", err)
	}
	value, plugin, err := Priority(context.Background(), stack, ask)
	if err != nil || value != "fast" || plugin.CapabilityID != "fast" {
		t.Fatalf("got %q from %q, %v; want the layer below the timed-out one", value, plugin.CapabilityID, err)
	}
}

func TestPriorityStopsWhenCallerContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	called := false
	stack := NewStack("p",
		layer("cancels", answerFunc(func(ctx context.Context) (string, error) {
			cancel()
			return "", ctx.Err()
		})),
		layer("below", answerFunc(func(context.Context) (string, error) {
			called = true
			return "late", nil
		})),
	)
	_, _, err := Priority(ctx, stack, ask)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if errors.Is(err, ErrNoAnswer) {
		t.Fatalf("err = %v, a canceled caller is not a missing answer", err)
	}
	if called {
		t.Fatal("a layer was called after the caller's context ended")
	}
}

func TestAllCallsEveryLayerInOrder(t *testing.T) {
	boom := errors.New("boom")
	stack := NewStack("p",
		layer("a", says("one")),
		layer("b", fails(boom)),
		layer("c", fails(ErrPass)),
		layer("d", says("two")),
	)
	results := All(context.Background(), stack, ask)
	if len(results) != 4 {
		t.Fatalf("got %d results, want one per layer", len(results))
	}
	wantIDs := []string{"a", "b", "c", "d"}
	for i, r := range results {
		if r.Plugin.PluginID != wantIDs[i] {
			t.Fatalf("result %d is from %q, want %q", i, r.Plugin.PluginID, wantIDs[i])
		}
	}
	if !results[0].Answered() || results[0].Value != "one" || !results[3].Answered() || results[3].Value != "two" {
		t.Fatalf("answers = %+v, %+v", results[0], results[3])
	}
	if results[1].Answered() || !errors.Is(results[1].Err, boom) {
		t.Fatalf("failed layer result = %+v", results[1])
	}
	if results[2].Answered() || !errors.Is(results[2].Err, ErrPass) {
		t.Fatalf("passing layer result = %+v", results[2])
	}
}

func TestAllSkipsRemainingLayersWhenCallerContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	called := false
	stack := NewStack("p",
		layer("cancels", answerFunc(func(context.Context) (string, error) {
			cancel()
			return "done", nil
		})),
		layer("below", answerFunc(func(context.Context) (string, error) {
			called = true
			return "late", nil
		})),
	)
	results := All(ctx, stack, ask)
	if !results[0].Answered() || !errors.Is(results[1].Err, context.Canceled) || called {
		t.Fatalf("results = %+v, below called = %v", results, called)
	}
}

func TestLayerCallsAreObservedAndCarryTheirCall(t *testing.T) {
	boom := errors.New("boom")
	var events []Event
	point := NewPoint[answerer]("test.answer", WithObserver(func(e Event) { events = append(events, e) }))

	var seen []Call
	record := func(value string, err error) func() answerer {
		return func() answerer {
			return answerFunc(func(ctx context.Context) (string, error) {
				call, ok := CallFrom(ctx)
				if !ok {
					t.Error("layer call context carries no Call")
				}
				seen = append(seen, call)
				return value, err
			})
		}
	}
	point.Bundled("passes", record("", ErrPass))
	point.Bundled("breaks", record("", boom))
	point.Bundled("answers", record("ok", nil))
	source := StackSourceFunc(func(context.Context, Scope) ([]Entry, error) {
		return []Entry{
			{Plugin: PluginRef{CapabilityID: "passes"}, Bundled: true},
			{Plugin: PluginRef{CapabilityID: "breaks"}, Bundled: true},
			{Plugin: PluginRef{CapabilityID: "answers"}, Bundled: true},
		}, nil
	})
	scope := Scope{Kind: "library", ID: "3"}
	stack, err := point.Bind(source, nil).Stack(context.Background(), scope)
	if err != nil {
		t.Fatalf("Stack: %v", err)
	}
	if _, _, err := Priority(context.Background(), stack, ask); err != nil {
		t.Fatalf("Priority: %v", err)
	}

	wantOutcomes := []Outcome{OutcomePassed, OutcomeFailed, OutcomeAnswered}
	if len(events) != len(wantOutcomes) || len(seen) != len(wantOutcomes) {
		t.Fatalf("got %d events and %d calls, want %d of each", len(events), len(seen), len(wantOutcomes))
	}
	for i, e := range events {
		if e.Outcome != wantOutcomes[i] || e.Point != "test.answer" || e.Scope != scope || e.Mode != "priority" {
			t.Fatalf("event %d = %+v", i, e)
		}
		if seen[i].Plugin != e.Plugin || seen[i].Point != e.Point || seen[i].Scope != scope {
			t.Fatalf("call %d = %+v, event plugin %+v", i, seen[i], e.Plugin)
		}
	}
	if events[0].Err != nil || !errors.Is(events[1].Err, boom) {
		t.Fatalf("event errors = %v, %v; only the failure carries one", events[0].Err, events[1].Err)
	}
}
