# Plugin host: extension points and stacks

`internal/extension` decides which implementations of a contract are called, and in what order.
It does not start or supervise plugin processes; that stays in `internal/pluginhost`.

No feature uses this package yet. Metadata providers are the first planned caller.

## Vocabulary

- **Extension point.** A named contract: a Go interface `T` plus the registry of the
  implementations of `T` compiled into the server. A package declares its point as a
  package-level `extension.NewPoint[T]("name")` in the package that owns the behavior.
- **Bundled implementation.** An implementation compiled into the server, registered from
  `init` with `point.Bundled(capabilityID, constructor)`.
- **Layer.** One plugin's implementation at one position. A layer is either bundled or reached
  through the point's remote adapter, which wraps an out-of-process plugin capability as a `T`.
- **Stack.** The ordered layers configured for one scope, top first. A point with a single stack
  uses `extension.Global`; a point with one stack per library uses a `Scope` that names it.
- **Stack source.** Where a point's configured layers are stored. Each point supplies its own, so
  a point keeps the storage it already has. The source leaves out disabled plugins.

A package binds its point once at startup, `point.Bind(source, remote)`, and asks the binding for
a stack per call or per unit of work.

## Dispatch modes

A point's contract says which mode each of its operations uses. One point may use several.

| Mode | Helper | Behavior |
| --- | --- | --- |
| None | `One` | The stack must hold exactly one layer. |
| Priority | `Priority` | Ask each layer, top first. The first answer wins. |
| All | `All` | Call every layer, top first. The caller combines the results as the contract says. |
| Route | `Route` | The caller's rule picks one layer; the first match wins. |

Notifications are an All operation whose results are discarded.

## Invariants

- **Order is the stack's order.** `Priority`, `All`, and `Route` visit layers top first, and `All`
  returns one result per layer in that order. Layers are called one at a time.
- **Any layer may pass.** A layer declines a call by returning `extension.ErrPass`. This includes
  bundled layers. A pass is not a failure and is not reported as one.
- **A failing layer does not fail the call.** In `Priority` a layer that fails or times out is
  skipped and the next is asked. In `All` its result carries the error and the rest still run.
  When no layer answers, `Priority` returns an error that wraps `ErrNoAnswer` and every layer
  failure; the caller decides what a missing answer means.
- **A layer that cannot be resolved is left out.** An entry with no bundled constructor, no remote
  adapter, or a plugin that will not start is logged and skipped. Resolving a stack fails only
  when its source fails.
- **The caller's context ends the call.** When the caller's context is canceled or expires, no
  further layer is called. `Priority` returns the context's error, not `ErrNoAnswer`.
- **The timeout is per layer.** `WithTimeout` sets a deadline on the context passed to each layer
  call. It bounds a layer only if the layer honors its context, which every remote call does.
- **Each layer call knows who it is.** The context handed to a layer call carries an
  `extension.Call` naming the point, scope, and plugin, and the point's observer receives one
  `Event` per layer call with its outcome and duration.

## Not here yet

The package does not cool off a layer that keeps failing; each call asks it again. It also has no
shared storage for stacks. Both arrive when a point needs them.
