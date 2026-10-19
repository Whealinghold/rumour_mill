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
    Discriminators map[string]func(C) string // optional named sources for add_fact discriminator_from; nil entry is ErrBadBindings
    ArgNames       ArgNames                 // optional arg renames; zero value = defaults
}
type ArgNames struct{ Discriminator, DiscriminatorFrom string } // json/yaml: discriminator, discriminator_from; empty = default
func DefaultArgNames() ArgNames                                  // discriminator, discriminator_from
func (c *Catalog[C]) RegisterKnowledgeOps(b Bindings[C]) error
```

Registers exactly two names:

| Name | Kind | Args | Behaviour |
| --- | --- | --- | --- |
| `knows` | condition | `type` (req); `about_subject` bool, default false; `min_confidence` float, default store floor; `source` string or list, default any | True if the store has an unexpired matching fact (`>=` the floor). With `about_subject` the fact's subject must equal `Bindings.Subject(C)`. |
| `add_fact` | action | `type`, `confidence`, `source` (req); `ttl` ticks, default none; `private` bool, default false; `per_event` bool, default false; `discriminator` string; `discriminator_from` string (a key of `Bindings.Discriminators`) | Adds a fact about `Bindings.Subject(C)` observed at `Bindings.Now(C)`. With `per_event` the discriminator is `Bindings.EventID(C)`, so each occurrence is distinct; otherwise repeats merge under the store's policy. `discriminator` sets the identity to a literal; `discriminator_from` to the named source's result (an empty result means the default identity; an unregistered name is `ErrBadArgs` listing the registered names at load; many distinct results make many facts, bounded by `MaxFacts`). At most one of `discriminator`, `discriminator_from` and `per_event` may be given (`ErrBadArgs`). A literal containing U+001F fails at run time with `knowledge.ErrInvalidFact`. `Prov.EventID` is set when `EventID` is provided. |

`ArgNames` renames the two optional args (wiring, not `Config`); a renamed arg's default name is then unknown (`ErrBadArgs`). Renames that equal each other or any base arg of `add_fact` or `knows` are `ErrBadBindings`, as is a nil `Discriminators` entry. Allow-lists live in one helper per action, so later optional args are added in one place.

Observing `add_fact` is done at the store, not in `Bindings`: `knowledge.WithHooks(Hooks{OnAdd})` and `knowledge.WithLogger` see every successful add (including spread deliveries and inference, which `add_fact`-only observation could not), so a high-cardinality `discriminator_from` under `MaxFacts` shows up as evictions there. See [knowledge](knowledge.md).

Everything else (same place as the event, dislikes the actor) is registered by the caller; plain attribute comparisons (role checks) can use the optional attribute conditions below. The library ships no domain names.

### Attribute conditions (optional)

Relational checks that are only an attribute comparison ("the listener has role X", "teller and listener share a department") need no hand-written condition. The caller supplies one lookup function:

```go
type Attributes[C any] struct {
    Sides  []string                                // side names a data file may use; required, non-empty
    Lookup func(ctx C, side, key string) (any, bool) // required; key comes from data, so treat it as untrusted
}
type AttributeNames struct{ Equals, In, Same string } // json/yaml: equals, in, same; empty = default
func DefaultAttributeNames() AttributeNames          // attr_equals, attr_in, attr_same
func (c *Catalog[C]) RegisterAttributeOps(a Attributes[C], n AttributeNames) error
```

Registers exactly three conditions, named by `n` (wiring, not part of `Config` or `Document`):

| Default name | Args | True when |
| --- | --- | --- |
| `attr_equals` | `side`, `key`, `value` (any JSON type incl. null, all required) | the attribute equals `value` |
| `attr_in` | `side`, `key`, `values` (non-empty list) | the attribute equals any of `values` |
| `attr_same` | `side`, `key`, `other_side` (required); `other_key` default `key` | both attributes exist and are equal |

Equality: numbers of any int, uint or float kind (and loaded JSON numbers) compare by float64 value, so integers above 2^53 lose precision; other values compare with `==`; values that cannot be compared (slices, maps, structs holding them) are never equal and never panic. A missing attribute (`Lookup` returns false) makes every condition false, so `not` over it is true. `side` is checked against `Sides` when the file loads. Registration is all-or-nothing: `ErrBadBindings` for missing `Sides`/`Lookup`, `ErrDuplicateName` for equal, taken or reserved names. The closures hold no mutable state.

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

`ErrBadArgs` (missing, mistyped, out-of-range or unknown arg), `ErrUnknownName`, `ErrWrongKind` (condition name where an action is required or vice versa), `ErrBadNode` (children on a registered name, `not` with two children, `if` without `cond`/`then`), `ErrDuplicateName` (duplicate or reserved name), `ErrBadName` (empty name or nil factory), `ErrBadBindings` (a required `Bindings` func is nil, or `Attributes.Sides`/`Lookup` missing; attribute conditions add no new sentinel), `ErrNoStore`. Resolution errors start with a path: `node` for `Condition`/`Action`, `nodes[i]` for the list helpers, `rules[i].when`/`rules[i].do[j]` for `LoadRules`, extended with `.of[k]`, `.cond`, `.then`, `.else`. All wrap with `%w`.

## 4. Concurrency and determinism

`Catalog` guards its maps with an `RWMutex`; resolution takes the read lock. Conditions and actions built from it are safe if the caller's registered closures are. Loading is deterministic: errors are reported in document order.

## 5. Testing

Table tests cover every error above. `FuzzLoad` (arbitrary bytes through `Load`, `LoadRules`, the filter catalog and `infer.New`) and `FuzzNode` (arbitrary JSON through `Catalog.Condition`/`Action`) must never panic; a 25-second run of each found nothing. Attribute conditions are covered by `attributes_test.go`: registration and renaming, equality and missing-attribute semantics, load-time errors with paths, a race test, and `Example_attributes`.

## 6. Worked examples

Five runnable `Example_` tests in `declare/` load embedded JSON from `declare/testdata/`:

- `spaceport.json` (`Example_spaceport`): custom source kinds, `default_priority`, a spreading filter built from `not`/`all`, a one-rule inference.
- `office.json` (`Example_office`): `stop: all`, a manager filter, `on_action_error`, a two-pass inference chain with `max_passes: 2`.
- `noir.json` (`Example_noir`): an `if`/`else` node with a nested `seq`, private facts, an inference restricted to `testimony` sources.
- `office_depts.json` (`Example_officeDepts`): the built-in attribute conditions, an `attr_equals` rule per department and a `not(attr_same{teller, listener, dept})` spreading filter, with `being.attrs` as the attribute source.
- `ward.json` (`Example_ward`): the generality proof, a hospital-ward staff sim written entirely as data plus the Go that registers its names; see section 7.

The first three and `office_depts` share a small generic caller type (`being`, `scene`) defined in `helpers_test.go`; the ward uses its own types and its own registrations.

## 7. Generality proof: the hospital ward

`testdata/ward.json` is 49 lines of data (config, three rules, one spreading filter, two chained inference rules). The Go it needs (`ward_test.go`, "ward setup") is 48 non-blank lines, down from 50 before the built-in attribute conditions: a `staff` type that satisfies `spread.Holder`, a `call` context, four `Bindings` one-liners, two `RegisterAttributeOps` calls (one per context type, each a `Lookup` closure over `staff.role`), two layers, and the wiring calls. The hand-written `role_is` and `listener_role_is` conditions and the `roleIn` helper are gone; `ward.json` uses `attr_in` with `side`/`key`/`values`. The saving is small (2 lines) because a second context type needs a second registration. No library change beyond the new built-in attribute ops was needed for the ward, which is rewritten to use them with no ward-specific condition code.