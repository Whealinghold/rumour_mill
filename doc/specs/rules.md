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
func (r *Registry[C]) Observe(h Hooks)           // chained; safe at any time; applies from the next Process
func (r *Registry[C]) SetLogger(l *slog.Logger)  // nil ignored; default discards
func (r *Registry[C]) SetClock(now func() time.Time) // enables Hooks.OnProcessDone; nil clears it (default: no timing)
func (r *Registry[C]) Layers() []string          // layer names in registration order; a fresh slice
func (r *Registry[C]) RuleCount(layer string) int // rules registered in layer across all scopes; 0 for an unknown layer

type Hooks struct {
    OnProcess     func(Result, error)       // once per Process, at the end
    OnProcessDone func(time.Duration)       // after OnProcess; only when SetClock was given
    OnRegister    func(RegisterEvent)       // after every Register call, successful or not
    OnUnregister  func(UnregisterEvent)     // after every Unregister call
}

// RegisterEvent describes one Register call. Count is the number of rules in
// Layer after the call (0 when the layer is unknown). Err is the error Register
// returned, nil on success; classify it with errors.Is against ErrDuplicateRule,
// ErrInvalidRule and ErrUnknownLayer.
type RegisterEvent struct {
    Layer, Scope, RuleID string
    Count                int
    Err                  error
}

// UnregisterEvent describes one Unregister call. Removed is its return value;
// Count is the number of rules in Layer after the call.
type UnregisterEvent struct {
    Layer, Scope, RuleID string
    Removed              bool
    Count                int
}

type Result struct {
    Fired   []Fired      // in execution order
    Stopped StopMode     // StopAll if Process ended early, else StopLayer if some layer was cut short, else StopNone
    Layers  []LayerStats // one entry per layer Process reached, in layer order
}

// Fired identifies a rule that fired. Failed is true when its Do returned an error.
type Fired struct {
    Layer, Scope, RuleID string
    Failed               bool
}

// LayerStats are the diagnostics of one layer in one Process call. Every count
// is a number of rules, never of scopes or events, so cardinality is bounded by
// the number of layers.
type LayerStats struct {
    Layer      string
    Candidates int // rules registered for the event type under the context's scope keys: the list the layer would walk
    Skipped    int // candidates whose When returned false
    Failed     int // fired candidates whose Do returned an error
}

// Unmatched reports whether no layer had any candidate rule for the event type,
// the signature of a typo'd or never-registered event type. It is true when
// Layers is empty (a registry without layers has no candidates either).
func (r Result) Unmatched() bool
```

`Candidates - Skipped - (fired in this layer)` is the number of candidates never reached because a `Stop` ended the layer or `Process` early. `Layers` has an entry for the layer being walked when `StopAll` or the `stop` error mode ends `Process`, and none for later layers; a layer with no candidates still has an entry (all zeros but the name). The `Layers` slice is freshly allocated per call and owned by the caller.

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

**Observability.** `Observe` adds hooks that run in the order given, with no registry lock held, after each `Process` (including the early `stop` return), receiving the `Result` and the returned error; every `Hooks` field is optional and each non-nil field is chained independently. `OnProcessDone(d)` runs after `OnProcess`, only when `SetClock(now)` was given; the clock is read twice per `Process` and never otherwise, and `d` is floored at 0 (duration is not a `Result` field, so `Result`s stay deterministic and comparable). `OnRegister` runs after every `Register` call, including failures (`RegisterEvent.Err` carries the sentinel); `OnUnregister` after every `Unregister` call (`Removed == false` for an unknown rule). Both run after the registry lock is released, with `Count` computed under the lock, as plain synchronous calls on the caller's goroutine. Concurrent `Register` calls may therefore deliver their events out of order; `Count` in each event is still the correct count at that call's moment, and an adapter that wants the current value reads `RuleCount`. `Layers()` and `RuleCount(layer)` let an adapter seed per-layer series at wiring time and read the rule counts already registered (`Layers` is immutable after `NewRegistry`; `RuleCount` takes the read lock). `Register` and `Unregister` each log a Debug line (layer, scope, rule id, ok) through the registry logger; `Unregister` logs ok=false when nothing was removed, and a failed `Register` logs nothing (the error is returned, and `OnRegister` reports it). `SetLogger` logs a Debug summary per `Process` (event type, fired count, the fired rule IDs as `layer/scope/id` strings under `rules`, per-layer `considered`/`skipped`/`failed`, `unmatched`, stopped mode) and a Warn when it returns an error. Both are read-only observers and never change the outcome.

Diagnosing "why did my rule not fire": `Result.Unmatched()` true means no rule is registered for that event type under any of the context's scopes (typo, or rules not loaded; compare `RuleCount`). Otherwise `Layers[i].Candidates > 0` with `Skipped == Candidates` means every candidate's `When` was false. `Candidates == 0` for one layer means no rule of that layer matches this context's scope keys (the context's `Scopes` returned the wrong keys, or the rules were registered under another scope).

Worked example (neutral domain, an office whose "floor" layer is keyed by department):

```go
reg, _ := rules.NewRegistry[*Ctx](rules.DefaultConfig(),
    rules.Layer[*Ctx]{Name: "all"},
    rules.Layer[*Ctx]{Name: "floor", Scopes: func(c *Ctx) []string { return []string{c.Dept} }})
reg.Register("all", "", rules.Rule[*Ctx]{ID: "log", Event: "coffee_spilled", Do: logIt})
reg.Register("floor", "finance", rules.Rule[*Ctx]{ID: "mop", Event: "coffee_spilled", When: isNearby, Do: mopUp})
res, _ := reg.Process(&Ctx{Dept: "finance"}, "cofee_spilled") // typo
// res.Unmatched() == true; res.Layers == [{all 0 0 0} {floor 0 0 0}]
res, _ = reg.Process(&Ctx{Dept: "finance", Near: false}, "coffee_spilled")
// res.Fired == [{all "" log false}]; res.Layers == [{all 1 0 0} {floor 1 1 0}]: "mop" was considered, skipped (isNearby false)
```

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
