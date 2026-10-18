# Spec: rules

Packages: `logic`, `rules` (standard library only). Status: approved with changes; `logic` and `rules` are implemented (Phase 3). The worked examples below are real `Example_` tests in `rules/example_test.go` (spaceport, office, noir).

## 1. Purpose

A layered rule registry and pipeline deciding what an entity does or learns when an event reaches it, plus the generic condition/action types and combinators. The layer set, the context type, and every condition and action are caller-defined. Loading rules from data is a separate package: [declarative loading](declarative.md).

## 2. The context type `C`

The reference passed three fixed interfaces (entity reader, entity writer, world reader) baking in factions, rooms, zones, players, dispositions and a calendar. Here the library is generic over one caller-defined type, the only place type parameters are used in the library:

```go
type Condition[C any] func(ctx C) bool
type Action[C any]    func(ctx C) error
```

`C` is whatever conditions and actions need to see: typically a struct holding the entity being processed, the event, and the world. The library never looks inside `C` except through functions the caller supplies (`Layer.Scopes`).

## 3. Public API: `logic`

```go
func All[C any](cs ...Condition[C]) Condition[C]   // every condition true; All() is true
func Any[C any](cs ...Condition[C]) Condition[C]   // any true; Any() is false
func Not[C any](c Condition[C]) Condition[C]
func Sequence[C any](as ...Action[C]) Action[C]    // in order; stops at and returns the first error
func If[C any](cond Condition[C], then, otherwise Action[C]) Action[C] // otherwise may be nil
```

Evaluation short-circuits left to right. A nil condition is treated as always-true and a nil action as a no-op, rather than panicking.

## 4. Public API: `rules`

```go
// Layer is one tier of rules. Layers are ordered; earlier layers are processed first.
type Layer[C any] struct {
    Name   string
    Scopes func(ctx C) []string // scope keys this context belongs to; nil = the single key ""
}

type StopMode string
const (
    StopNone  StopMode = ""      // continue
    StopLayer StopMode = "layer" // skip the remaining rules in this layer
    StopAll   StopMode = "all"   // skip the remaining rules in this and all later layers
)

type Rule[C any] struct {
    ID       string       // unique within (layer, scope); required
    Event    string       // event type; required; exact match
    When     logic.Condition[C] // nil = always
    Do       logic.Action[C]    // nil = no-op (a rule may exist only to stop propagation)
    Priority int          // lower runs first
    Stop     StopMode
}

type Config struct {
    OnActionError string `json:"on_action_error" yaml:"on_action_error"` // "continue" | "stop"
}
const (ContinueOnError = "continue"; StopOnError = "stop")
func DefaultConfig() Config
func (c Config) Validate() error

type Registry[C any] struct{ /* unexported */ }

func NewRegistry[C any](cfg Config, layers ...Layer[C]) (*Registry[C], error)
func (r *Registry[C]) Register(layer, scope string, rule Rule[C]) error
func (r *Registry[C]) Unregister(layer, scope, ruleID string) bool
func (r *Registry[C]) Process(ctx C, eventType string) (Result, error)

type Result struct {
    Fired   []Fired  // in execution order
    Stopped StopMode // StopAll if Process ended early, else StopLayer if some layer was cut short, else StopNone
}
type Fired struct{ Layer, Scope, RuleID string }

var ErrUnknownLayer, ErrInvalidLayer, ErrInvalidRule, ErrDuplicateRule, ErrInvalidConfig error
```

A layer with a nil `Scopes` has exactly one scope, the empty string (the "universal" layer), and `Register` rejects any other scope for it with `ErrInvalidRule`. `Scopes` is called each time `Process` reaches that layer, not once up front, so it sees changes earlier layers' actions made to `C`. The classic three tiers are `Layer{Name: "all"}`, a group-keyed layer, and `Layer{Name: "individual", Scopes: func(c Ctx) []string { return []string{c.Self.ID} }}`; a simulation with a fourth tier just adds a layer.

| Config field | Default | Meaning |
| --- | --- | --- |
| `OnActionError` | `"continue"` | When an action errors: record and carry on, or stop at once. The empty string means "continue", so the zero `Config{}` works; any other unknown value is invalid. |

`DefaultPriority` (100) is not a `rules` tunable: only the declarative loader needs it, so it lives with the loader (see [declarative](declarative.md) and [config](config.md)). Code-registered rules always use `Rule.Priority` as written; zero is a real priority. The zero `Config{}` is valid and behaves like `DefaultConfig()`.
## 5. Behaviour: `Process(ctx, eventType)`

1. For each layer in registration order:
   1. `keys := layer.Scopes(ctx)` (or `[""]`), dropping duplicates (first wins).
   2. Gather the rules registered under `(layer, key, eventType)` for each key and merge them into one list ordered by `Priority` ascending, then position of the key in `keys`, then registration order. (An entity in two groups gets both groups' rules interleaved by priority; this is what makes `StopLayer` well defined.)
   3. Walk the list. A rule **fires** if `When == nil || When(ctx)`: run `Do`, record `Fired`, then apply `Stop`.
   4. `StopLayer` ends this layer's walk; later layers still run. `StopAll` ends `Process`. `Result.Stopped` records which.
2. A rule whose condition is false never fires and never stops anything.
3. A `Do` error is joined (`errors.Join`) into the returned error. With `continue` the walk goes on (a rule that fired but errored still applies its `Stop`). With `stop`, `Process` returns right after the failing rule with `Result.Stopped == StopNone`.

Rule lists are snapshotted under the read lock before caller code runs, so a condition or action may call `Register`/`Unregister` without deadlock; the change applies from the next `Process`. No rules for the event type is not an error.

## 6. Error cases

`ErrUnknownLayer` (register into a layer the registry lacks); `ErrInvalidLayer` (empty or duplicate layer name); `ErrInvalidRule` (empty `ID` or `Event`, invalid `Stop`, non-empty scope on a universal layer); `ErrDuplicateRule` (same `(layer, scope, ID)`; IDs are unique across event types within a scope); `ErrInvalidConfig`. Joined action errors come back from `Process`. All wrap sentinels with `%w`.

## 7. Concurrency and determinism

`Registry` is safe for concurrent use: `Register`/`Unregister` take the write lock; `Process` takes the read lock only to snapshot, then runs without a lock. The library never mutates `ctx`; actions do, and the caller synchronises what `ctx` points at. Evaluation order is fully specified above; nothing here uses time or randomness.

## 8. Original hardcoded thing → how it is configurable here

| Original | Here |
| --- | --- |
| `RuleLayer` enum: universal, faction, individual | Caller-defined ordered `Layer[C]` list |
| Three `Register*Rule` functions | One `Register(layer, scope, rule)` |
| Registry keyed by `FactionID`/`EntityID` types | Opaque string scope keys from `Layer.Scopes(ctx)` |
| `StopPropagation bool` (stops within one layer) | `Stop`: `none`/`layer`/`all`; `layer` reproduces the reference |
| Priority `int`, lower first, no default | Same ordering with fully specified tie-breaks; the `100` default for data-loaded rules lives in the declarative loader |
| `EntityReader`/`EntityWriter`/world-reader interfaces (faction, room, zone, player flag, disposition, relationships, combat behaviour, threat level, market-day flag, season) | None exist. The caller's `C` and conditions/actions expose whatever the host world has |
| Built-in conditions (same room/zone, attitude thresholds `-25`/`25`, relationship checks, profession, resource codes, commodity, season) | Not shipped; caller registers equivalents, thresholds become `args` |
| Built-in actions (disposition, goals, supply stubs) | Not shipped; stubs dropped |
| Typed event payloads inspected by conditions | Events live in the caller's `C` |
| Case-insensitive value matching | Exact matching; callers normalise |
| Four `AddKnowledge*` actions fixing source and secrecy | One `add_fact` operation in [declarative loading](declarative.md) |
| `ActionFn` could not fail | `Action[C]` returns `error`; `OnActionError` |
| `All/Any/Not/Sequence/If` | Kept in `logic` |
| Mutex held while rules were read live | Snapshot then run |

## 9. Worked examples

Three runnable `Example_` tests in `rules/example_test.go`, in unrelated domains over the same `Registry` code:

- **Spaceport** (`Example_spaceport`): layers station (universal), guild (multi-key: a crew member in several guilds), individual. A customs guild rule with a lower priority number and `StopLayer` suppresses the lower-priority guild rules, while the individual layer still runs.
- **Office** (`Example_office`): layers company, department, person. A finance rule with `StopAll` ends processing, so the later person layer is skipped for finance staff and reached by everyone else; `Result.Stopped` reports it.
- **Noir detective** (`Example_noir`): equal priorities run in registration order; a failing action is joined into the returned error without stopping the pipeline (default `continue`); `Unregister` removes a rule.

The tests in `rules/rules_test.go` cover priority ordering, layer ordering, multi-key scopes, each stop mode, error policy, registration errors, per-layer `Scopes` evaluation, re-entrant `Register`, determinism and concurrent use (`-race`).
