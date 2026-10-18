# Spec: declarative loading

Package: `declare`. Status: approved with changes; implemented (Phase 5). Depends on every core package; no core package depends on it (enforced by `architecture_test.go`).

## 1. Purpose

Lets rules, spreading filters and inference rules be written as data (YAML or JSON) and bound to caller-registered, named conditions and actions, so behaviour changes without recompiling. Optional: a caller who builds everything in Go never imports this package. The document and config format are in [config](config.md).

## 2. Public API

### Args

```go
// Args are the free-form parameters of a named condition/action, as decoded from data.
type Args map[string]any

func (a Args) String(key string) (string, error)        // required
func (a Args) StringOr(key, def string) (string, error)
func (a Args) Strings(key string) ([]string, error)     // string or list of strings; absent -> nil
func (a Args) Float(key string) (float64, error)
func (a Args) FloatOr(key string, def float64) (float64, error)
func (a Args) Int(key string) (int64, error)            // integer-valued number
func (a Args) IntOr(key string, def int64) (int64, error)
func (a Args) Bool(key string) (bool, error)
func (a Args) BoolOr(key string, def bool) (bool, error)
func (a Args) Unknown(allowed ...string) error          // error naming any key not in allowed
```

Getters return an error wrapping `ErrBadArgs` for a missing required key or wrong type. Factories should end with `a.Unknown(...)` so typos in data files fail loudly.

### Node

```go
// Node is one expression in a data file.
type Node struct {
    Use  string `json:"use" yaml:"use"`                       // registered name or reserved combinator
    Args Args   `json:"args,omitempty" yaml:"args,omitempty"` // for a registered name
    Of   []Node `json:"of,omitempty" yaml:"of,omitempty"`     // children for all / any / not / seq
    Cond *Node  `json:"cond,omitempty" yaml:"cond,omitempty"` // for if
    Then *Node  `json:"then,omitempty" yaml:"then,omitempty"` // for if
    Else *Node  `json:"else,omitempty" yaml:"else,omitempty"` // for if
}
```

### Catalog

An instance (no package-level registry) mapping names to factories, safe for concurrent use.

```go
type Catalog[C any] struct{ /* unexported */ }

func NewCatalog[C any]() *Catalog[C]

func (c *Catalog[C]) RegisterCondition(name string, f func(Args) (logic.Condition[C], error)) error
func (c *Catalog[C]) RegisterAction(name string, f func(Args) (logic.Action[C], error)) error

func (c *Catalog[C]) Condition(n Node) (logic.Condition[C], error)
func (c *Catalog[C]) Action(n Node) (logic.Action[C], error)
func (c *Catalog[C]) ConditionAll(ns []Node) (logic.Condition[C], error) // AND; empty -> nil (always true)
func (c *Catalog[C]) ActionSeq(ns []Node) (logic.Action[C], error)       // sequence; empty -> nil (no-op)
```

Reserved combinator names, in every catalog and not overridable:

| `use` | Kind | Shape |
| --- | --- | --- |
| `all`, `any` | condition | `of: [cond...]` |
| `not` | condition | `of: [cond]` (exactly one) |
| `seq` | action | `of: [action...]` |
| `if` | action | `cond`, `then`, optional `else` |

Conditions and actions have separate namespaces. Whether a node is a condition or an action is determined by where it appears (`when`, `cond`, filter lists, `of` under `all`/`any`/`not` → condition; `do`, `then`, `else`, `of` under `seq` → action).

### Knowledge operations (optional)

Knowledge-aware operations need the holder's store, the tick and the event subject, which live in the caller's `C`:

```go
type Bindings[C any] struct {
    Store   func(C) *knowledge.Store // nil result: conditions are false, actions return ErrNoStore
    Now     func(C) knowledge.Tick
    Subject func(C) string           // default subject for add_fact (often the event's actor)
    EventID func(C) string           // optional; provenance and per_event identity
}
func (c *Catalog[C]) RegisterKnowledgeOps(b Bindings[C]) error
```

Registers exactly two names:

| Name | Kind | Args | Behaviour |
| --- | --- | --- | --- |
| `knows` | condition | `type` (req); `about_subject` bool, default false; `min_confidence` float, default store floor; `source` string or list, default any | True if the store has an unexpired matching fact (`>=` the floor). With `about_subject` the fact's subject must equal `Bindings.Subject(C)`. |
| `add_fact` | action | `type`, `confidence`, `source` (req); `ttl` ticks, default none; `private` bool, default false; `per_event` bool, default false | Adds a fact about `Bindings.Subject(C)` observed at `Bindings.Now(C)`. With `per_event` the discriminator is `Bindings.EventID(C)`, so each occurrence is distinct; otherwise repeats merge under the store's policy. `Prov.EventID` is set when `EventID` is provided. |

Everything else (same place as the event, dislikes the actor, role checks) is registered by the caller. The library ships no such names.

### Loading rules

```go
type RuleSpec struct {
    ID       string   `json:"id" yaml:"id"`
    Layer    string   `json:"layer" yaml:"layer"`
    Scope    string   `json:"scope,omitempty" yaml:"scope,omitempty"` // omit for universal layers
    Event    string   `json:"event" yaml:"event"`
    Priority *int     `json:"priority,omitempty" yaml:"priority,omitempty"` // nil -> RulesConfig.DefaultPriority
    Stop     rules.StopMode `json:"stop,omitempty" yaml:"stop,omitempty"`
    When     *Node    `json:"when,omitempty" yaml:"when,omitempty"` // nil = always
    Do       []Node   `json:"do,omitempty" yaml:"do,omitempty"`     // implicit sequence
}

// LoadRules compiles specs against cat and registers them in reg, all-or-nothing.
// RulesConfig is rules.Config plus the loader-only DefaultPriority (default 100).
type RulesConfig struct {
    rules.Config
    DefaultPriority int `json:"default_priority" yaml:"default_priority"`
}

func LoadRules[C any](reg *rules.Registry[C], cfg RulesConfig, specs []RuleSpec, cat *Catalog[C]) error
```

`LoadRules` first resolves every spec's `when` and `do` nodes and reports all resolution errors together, each with its path, such as `rules[3].when.of[1]: declare: unknown name: condition "same_dok"`; if any fail, nothing is registered. It then registers the rules in order; if registration fails (unknown layer, empty ID or event, bad stop mode, a duplicate within the batch or against an earlier rule) the rules this call already registered are unregistered again and that error is returned, prefixed `rules[i] (id)`. Concurrent `Process` calls may briefly see a partial load.

### Spreading filters

Spreading filters are just conditions over `spread.Encounter`: build a `Catalog[spread.Encounter]`, register names, then `cat.ConditionAll(doc.Spreading.Filters)` and pass the result to `spread.WithFilter`.

## 3. Errors

`ErrBadArgs` (missing, mistyped, out-of-range or unknown arg), `ErrUnknownName`, `ErrWrongKind` (condition name where an action is required or vice versa), `ErrBadNode` (children on a registered name, `not` with two children, `if` without `cond`/`then`), `ErrDuplicateName` (duplicate or reserved name), `ErrBadName` (empty name or nil factory), `ErrBadBindings` (a required `Bindings` func is nil), `ErrNoStore`. Resolution errors start with a path: `node` for `Condition`/`Action`, `nodes[i]` for the list helpers, `rules[i].when`/`rules[i].do[j]` for `LoadRules`, extended with `.of[k]`, `.cond`, `.then`, `.else`. All wrap with `%w`.

## 4. Concurrency and determinism

`Catalog` guards its maps with an `RWMutex`; resolution takes the read lock. Conditions and actions built from it are safe if the caller's registered closures are. Loading is deterministic: errors are reported in document order.

## 5. Testing

Table tests cover every error above. `FuzzLoad` (arbitrary bytes through `Load`, `LoadRules`, the filter catalog and `infer.New`) and `FuzzNode` (arbitrary JSON through `Catalog.Condition`/`Action`) must never panic; a 25-second run of each found nothing.

## 6. Worked examples

Four runnable `Example_` tests in `declare/` load embedded JSON from `declare/testdata/`:

- `spaceport.json` (`Example_spaceport`): custom source kinds, `default_priority`, a spreading filter built from `not`/`all`, a one-rule inference.
- `office.json` (`Example_office`): `stop: all`, a manager filter, `on_action_error`, a two-pass inference chain with `max_passes: 2`.
- `noir.json` (`Example_noir`): an `if`/`else` node with a nested `seq`, private facts, an inference restricted to `testimony` sources.
- `ward.json` (`Example_ward`): the generality proof, a hospital-ward staff sim written entirely as data plus the Go that registers its names; see section 7.

The first three share a small generic caller type (`being`, `scene`) defined in `helpers_test.go`; the ward uses its own types and its own registrations.

## 7. Generality proof: the hospital ward

`testdata/ward.json` is 49 lines of data (config, three rules, one spreading filter, two chained inference rules). The Go it needs (`ward_test.go`, "ward setup") is 50 non-blank lines: a `staff` type that satisfies `spread.Holder`, a `call` context, four `Bindings` one-liners, one `role_is` condition, one `listener_role_is` filter condition, two layers, and the wiring calls. No file under `knowledge/`, `logic/`, `rules/`, `spread/`, `infer/` or `declare/*.go` was changed to support it.