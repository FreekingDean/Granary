# Plugin host: extensions, stacks, and layers

`internal/extension` decides which implementations of a contract are called, and in what order.
It does not start or supervise plugin processes; that stays in `internal/pluginhost`.

No feature uses this package yet. Metadata providers are the first planned caller.

## Vocabulary

- **Layer.** One implementation of a contract, with an id.
- **Extension.** A named contract (a Go interface `T`) and every layer available for it. A package
  declares its extension as a package-level `extension.New[T]("name")` and layers join it with
  `Register(id, impl)`.
- **Stack.** The ordered layers the server uses for a call, top first, and the helpers that
  choose among them. `ext.Stack(ids...)` builds one from registered layers.

## Choosing among layers

An extension's contract says which mode each of its operations uses. One extension may use several.

| Mode | Helper | Behavior |
| --- | --- | --- |
| None | `One` | The stack must hold exactly one layer. |
| Priority | `Priority` | Ask each layer, top first. The first answer wins. |
| All | `All` | Call every layer, top first. The caller combines the results as the contract says. |
| Route | `Route` | The caller's rule picks one layer; the first match wins. |

Notifications are an All operation whose results are discarded.

A call on a layer returns `(value, ok, err)`. `ok` false is a pass: the layer has nothing to say
about this call. `Priority` and `All` report each layer's `Outcome` as `Answered`, `Passed`, or
`Failed`.

## Invariants

- **Order is the stack's order.** `Priority`, `All`, and `Route` visit layers top first, and `All`
  returns one result per layer in that order. Layers are called one at a time.
- **A pass is not an error.** Any layer may pass. A zero value returned with `ok` true is an
  answer.
- **A failing layer does not fail the call.** In `Priority` a layer that fails is skipped and the
  next is asked. In `All` its result carries the error and the rest still run. When no layer
  answers, `Priority` reports `Passed` if every layer passed and `Failed` with the layer errors
  otherwise; the caller decides what a missing answer means.
- **An unregistered layer is left out.** `Stack` logs and skips an id with no registered layer.
- **The caller's context ends the call.** When the caller's context is canceled or expires, no
  further layer is called and the outcome is `Failed` with the context's error.
