# Spec: spreading and inference

Packages: `spread`, `infer` (standard library plus sibling packages). Status: approved with changes; `spread` and `infer` are implemented (Phase 4). Both are concrete, non-generic types. The worked examples below are real `Example_` tests (`spread/example_test.go`, `infer/example_test.go`) in a spaceport, an office and a noir domain. Zero values: the zero `spread.Config` and `infer.Config` are rejected by `Validate` (a zero decay factor would erase every fact; zero passes would do nothing; `skip_back_to_source` defaults to true so its zero value is not the default), so start from `DefaultConfig()`.

---

# Part A: Spreading

## A1. Purpose

Move facts from one holder (the teller) to others (listeners) with confidence decay, a minimum-confidence cut-off, and a caller-injected policy deciding who may tell whom what. There is no global filter registry: each `Spreader` carries its own filters. The caller passes the listeners; the library does not know about rooms or earshot.

## A2. Public API

```go
package spread

// Holder is the consumer-shaped interface a participant satisfies.
type Holder interface {
    ID() string
    Knowledge() *knowledge.Store // nil = holds nothing / cannot receive
}

// Encounter is what a filter judges: one fact, one teller, one listener.
type Encounter struct {
    Teller, Listener Holder
    Fact             knowledge.Fact // the teller's version, before decay
    Now              knowledge.Tick
}

// Transform distorts a fact in transit. It receives the same Encounter the
// filters judged (teller, listener, the teller's fact before decay, current
// tick) and the copy about to be delivered (decayed confidence, new source and
// provenance already applied), and returns what is actually delivered. It may
// change Confidence, Payload and Expiry. ID, Type, Subject, Discriminator,
// Source, Prov, Observed and Private (and FadedAt, which is reset to zero on
// the copy) are restored afterwards, so a transform cannot change what a fact
// is or forge its origin. Treat the Encounter, including
// Encounter.Fact.Prov.Premises, as read-only: the premises slice is shared
// across the deliveries of one Spread call, and this is documented, not
// enforced. Age is e.Now - e.Fact.Observed: ticks since the teller acquired or
// last upgraded the fact, not since the original event. A panic propagates.
// Transform is called synchronously on the caller's goroutine. It is not safe for
// concurrent use against a shared store.
type Transform func(e Encounter, delivered knowledge.Fact) knowledge.Fact

type Spreader struct {
    // Transform, if non-nil, is applied to every delivery that passed the
    // filters; see the Transform type. Nil leaves the delivered copy as built.
    Transform Transform
    /* unexported */
}

func New(cfg Config, opts ...Option) (*Spreader, error)
type Option func(*Spreader)
func WithFilter(c logic.Condition[Encounter]) Option // repeatable; all must pass

func (s *Spreader) Spread(teller Holder, listeners []Holder, now knowledge.Tick) (Result, error)

type Transfer struct {
    FactID     string
    To         string
    Confidence float64           // as delivered
    Outcome    knowledge.Outcome // what the listener's store did with it
    Hops       int               // Prov.Hops of the delivered copy (the teller's hops + 1)
}
// Dropped counts offers that did not become a delivery, by reason.
type Dropped struct {
    Filtered           int // a filter said no
    BackToSource       int // SkipBackToSource applied
    BelowMinConfidence int // decayed or transformed confidence under MinConfidence
    TransformDropped   int // Transform returned a NaN confidence
}

// Withheld counts FACTS (not fact-listener pairs) the teller held that were not
// offered at all, by reason. Not part of Offered or Dropped. Each fact is
// counted under exactly one reason, the first that applies in this order:
// expired, below_source_confidence, private, max_hops.
type Withheld struct {
    Private               int // unexpired, at or above MinSourceConfidence, and Private (also at MaxHops still counts here)
    MaxHops               int // unexpired, at or above MinSourceConfidence, not Private, and at Config.MaxHops
    BelowSourceConfidence int // unexpired, but under MinSourceConfidence (whether or not Private or at MaxHops)
    Expired               int // Expiry <= now, whatever else is true of the fact; dead facts awaiting Prune count on every call
}
// "private", "max_hops", "below_source_confidence", "expired", zero counts included
func (w Withheld) Each(f func(reason string, n int))

// SkippedListeners counts listeners passed to Spread that were skipped without
// any offer. The teller appearing in its own listener list is skipped silently
// and counted nowhere.
type SkippedListeners struct {
    Nil     int // a nil Holder in the listeners slice
    NoStore int // a listener whose Knowledge() is nil: it cannot receive
}
// "nil_listener" then "no_store", zero counts included
func (k SkippedListeners) Each(f func(reason string, n int))

// Offered counts every (fact, listener) pair considered, so
// Offered == len(Transfers) + sum(Dropped) + store errors (unchanged by Withheld).
type Result struct {
    Transfers []Transfer
    Offered   int
    Dropped   Dropped
    HolderMismatches int // Typed filter / TypedTransform met a holder of the wrong type
    Withheld  Withheld // facts the teller held but never offered
    SkippedListeners SkippedListeners // listeners skipped without any offer (nil, or no store)
}

type Hooks struct {
    OnSpread     func(Result, error)    // result plus joined store errors; once per Spread that ran
    OnSkip       func(reason string)    // a Spread that returned early: SkipNilTeller or SkipNoStore; OnSpread does NOT run
    OnSpreadDone func(time.Duration)    // after OnSpread; only when WithClock was given
}
const (
    SkipNilTeller = "nil_teller" // teller is nil
    SkipNoStore   = "no_store"   // teller.Knowledge() is nil
)
func SkipReasons() []string              // fresh slice: SkipNilTeller, SkipNoStore; adapters seed from it
func WithHooks(h Hooks) Option           // repeatable; each non-nil field chained independently, in the order given
func WithLogger(l *slog.Logger) Option   // nil ignored; default discards
func WithClock(now func() time.Time) Option // enables OnSpreadDone; nil ignored; default none = no timing, no overhead
```

Neutral example (office gossip). Alice tells three colleagues; one slot in the slice is nil and one colleague has no store:

```go
res, _ := sp.Spread(alice, []spread.Holder{bob, nil, carol, desklessTemp}, now)
// res.SkippedListeners == {Nil: 1, NoStore: 1}; Offered counts only bob and carol.
// A fact alice holds at confidence 0.2 (MinSourceConfidence 0.3) shows up as res.Withheld.BelowSourceConfidence == 1.
// A Transfer for bob now carries Hops: 2 when alice herself had heard it at 1 hop.
res, _ = sp.Spread(nil, listeners, now) // OnSkip(SkipNilTeller); OnSpread does not run; zero Result returned as before
```

**Observability.** `spread.WithHooks(Hooks{OnSpread func(Result, error)})` is called once per `Spread` with the `Result` and the joined store errors (nil when none); `spread.WithLogger(*slog.Logger)` logs one Debug summary per call and, when `Result.HolderMismatches > 0`, one Warn per call (default logger discards). `Result.HolderMismatches` counts how often a `Typed` filter or `TypedTransform` met a teller or listener of the wrong type during the call (a mismatched filter is also counted in `Dropped.Filtered`, since that wiring bug otherwise reads as policy; a mismatched transform passes the delivery through). `spread.OnMismatch(func(Encounter))` is the per-call `TypedOption` for the same event. `WithHooks` and `OnMismatch` are repeatable and chain in order, nil ignored. `Dropped.Each(func(reason string, n int))` visits every drop reason, so adapters need not list the fields. `Result.Withheld` counts facts (not pairs) the teller held that were not offered at all, each under exactly one reason (first match of: expired, below `MinSourceConfidence`, `Private`, reached `MaxHops`); they are in none of `Offered`, `Dropped` or the identity `Offered == len(Transfers) + Dropped total + store errors`, which is unchanged. `Withheld.Each` visits `private`, `max_hops`, `below_source_confidence`, `expired` in that order, zero counts included. To classify expiry and the source-confidence floor, `Spread` reads the teller's facts with `All()` and applies the same expiry (`Expiry != nil && *Expiry <= now`) and floor rules as `Find`; the offered set and its order are unchanged. `Result.SkippedListeners` counts nil listeners and listeners with no store (`Each` visits `nil_listener` then `no_store`) and `Spread` logs one Warn per call when either is non-zero; the teller in its own listener list stays a silent, uncounted skip. `Transfer.Hops` is the delivered copy's `Prov.Hops`. A nil teller or a teller with nil `Knowledge()` still returns early with a zero `Result` and WITHOUT calling `OnSpread` (there is no `Result` to report), but now calls `OnSkip(SkipNilTeller)` or `OnSkip(SkipNoStore)` (and still logs its Warn). `WithClock(now)` enables timing: `now` is read at the start of `Spread` (after the early-return checks) and again just before `OnSpread`, and `OnSpreadDone(d)` runs after `OnSpread` with `d` floored at 0; with no clock it is never called and the clock is never read. Durations are not a `Result` field, so `Result`s stay deterministic and comparable. The skip path calls neither `OnSpread` nor `OnSpreadDone`. Beyond the Debug summary (now including withheld and skipped listeners), `Spread` logs one Warn per call when there were store errors (teller, error count), one when `Dropped.TransformDropped > 0`, one when `SkippedListeners` is non-zero, and one for a nil teller or a teller with nil `Knowledge()`. Adds are observed at the store (`knowledge.WithHooks`). Ready-made Prometheus and OpenTelemetry adapters live in separate modules under `contrib/`. Hooks are synchronous, read-only and must not change the outcome; nil means no-op.

Filters are ordinary `logic.Condition`s over `Encounter`, written in Go or built from data (see [declarative](declarative.md)), and composable with `logic.All/Any/Not`. A filter needing the caller's concrete types wraps it in `spread.Typed` (below), which asserts `e.Teller`/`e.Listener` once, so the filter body works in terms of the caller's own holder type.

```go
// TypedEncounter is Encounter with the caller's holder type H in place of Holder.
type TypedEncounter[H Holder] struct {
    Teller, Listener H
    Fact             knowledge.Fact
    Now              knowledge.Tick
}

// Typed adapts f into an Encounter filter. If the teller or listener is not an H
// (including a nil Holder) the encounter is blocked and f is not called. A nil f
// returns a nil condition, which WithFilter ignores. H may be a pointer or an
// interface type. No state is held; concurrency is that of f.
func Typed[H Holder](f func(TypedEncounter[H]) bool, opts ...TypedOption) logic.Condition[Encounter]

// TypedTransform adapts f into a Transform. TypedEncounter.Fact is the teller's
// fact before decay; delivered is the copy about to be delivered. If the teller
// or listener is not an H (including nil) f is not called and delivered is
// returned unchanged. A nil f returns nil, leaving Spreader.Transform unset. The
// fixed-field restore above still applies to what f returns.
func TypedTransform[H Holder](f func(TypedEncounter[H], knowledge.Fact) knowledge.Fact, opts ...TypedOption) func(Encounter, knowledge.Fact) knowledge.Fact

// TypedOption configures Typed and TypedTransform; OnMismatch is its only option.
type TypedOption func(*typedOpts)
// OnMismatch calls f when a holder is not an H, before blocking or passing through.
func OnMismatch(f func(Encounter)) TypedOption
```

## A3. Config

```go
type Config struct {
    DecayFactor         float64 `json:"decay_factor" yaml:"decay_factor"`
    MinConfidence       float64 `json:"min_confidence" yaml:"min_confidence"`
    MinSourceConfidence float64 `json:"min_source_confidence" yaml:"min_source_confidence"`
    MaxHops             int     `json:"max_hops" yaml:"max_hops"`
    ReceivedKind        string  `json:"received_kind" yaml:"received_kind"`
    SkipBackToSource    bool    `json:"skip_back_to_source" yaml:"skip_back_to_source"`
}
func DefaultConfig() Config
func (c Config) Validate() error
```

| Field | Default | Meaning |
| --- | --- | --- |
| `DecayFactor` | `0.8` | Per-hop confidence multiplier, range `(0,1]`; `1` = no loss. |
| `MinConfidence` | `0.2` | After decay and transform, a copy below this is not delivered. |
| `MinSourceConfidence` | `0.3` | A teller only offers facts at or above this. Separate from `MinConfidence`: a fact must be fairly sure to be told but may arrive weak. |
| `MaxHops` | `0` | Maximum `Prov.Hops` a delivered copy may have; `0` = unlimited. A fact whose `Hops >= MaxHops` is not offered. |
| `ReceivedKind` | `"relayed"` | Source kind stamped on delivered copies; must be ranked by the caller's store config. |
| `SkipBackToSource` | `true` | Don't tell a listener a fact whose `Prov.From` is that listener (stops echo). |

## A4. Behaviour: `Spread(teller, listeners, now)`

1. If the teller is nil or `teller.Knowledge()` is nil, call `OnSkip` with `SkipNilTeller` or `SkipNoStore`, log a Warn and return an empty result (no `OnSpread`).
2. Candidate facts: every fact of the teller's store (`All()`) at `now`, classified in this order: expired (count `Withheld.Expired`); confidence `< MinSourceConfidence` (count `Withheld.BelowSourceConfidence`); `Private` (count `Withheld.Private`); `MaxHops > 0 && Prov.Hops >= MaxHops` (count `Withheld.MaxHops`); anything else is a candidate. Privacy is intrinsic and cannot be disabled by filters or config. Order: confidence descending, then ascending ID.
3. For each listener in the order given (a nil one counts `SkippedListeners.Nil`; one whose ID equals the teller's is skipped silently; one whose `Knowledge()` is nil counts `SkippedListeners.NoStore`), for each candidate:
   1. If `SkipBackToSource` and `Prov.From == listener.ID()`, skip.
   2. Run the filters on the `Encounter`, in the order added, short-circuiting; any `false` skips.
   3. Build the copy: same `ID`, `Type`, `Subject`, `Discriminator`, `Payload`, `Expiry`; `Confidence = fact.Confidence × DecayFactor`; `Source = ReceivedKind`; `Observed = now`; `Prov = {From: teller.ID(), Hops: fact.Prov.Hops+1, EventID: fact.Prov.EventID}` (premises not forwarded); `Private = false`.
   4. Apply `Transform` if set, passing the same `Encounter` the filters judged (built once per listener and fact) and the copy from step 3; then restore the fixed fields (see A2), clamp confidence to `[0,1]`. Position is unchanged: after the copy is built, before the `MinConfidence` check. The teller's store and the `Encounter` are not modified on the transform's behalf. The copy's `FadedAt` is reset to zero: the listener acquired the fact at `now`, so the teller's settle point does not carry over.
   5. If confidence `< MinConfidence`, skip.
   6. `listener.Knowledge().Add(copy)`; record a `Transfer` (with `Hops` = the copy's `Prov.Hops`) with the store's outcome. Normal merge rules apply, so a listener holding a better-ranked or stronger version is unaffected (`Rejected`).
4. The teller's store is never modified.

The teller's facts are read once up front; listener stores are written one at a time. Decay is per hop, not per time.

## A5. Error cases

`ErrInvalidConfig` from `New`: `DecayFactor` outside `(0,1]`; `MinConfidence` or `MinSourceConfidence` outside `[0,1]`; `MaxHops < 0`; empty `ReceivedKind`. `Spread` returns store errors (joined), for example `knowledge.ErrIdentityMismatch`; listeners already processed keep their facts (not transactional). Nil listeners, the teller itself and holders without a store are skipped. A nil filter is ignored. With `spread.Typed`, a teller or listener of the wrong type is blocked (counted in `Dropped.Filtered`), not an error, and not a panic. A transform that returns a NaN confidence drops that delivery; a nil `Transform` behaves as if unset. Panics from caller filters/transform propagate (nothing recovers them, so listeners already processed keep their facts).

## A6. Concurrency and determinism

`Spreader` holds no mutable state. It is **not safe for concurrent use against shared stores**, because `knowledge.Store` is not: `Spread` and `Store.Fade` (see [knowledge](knowledge.md)) must not run concurrently against the same store, and the `Transform` is called on the caller's goroutine. The caller serialises access (for example one lock per holder, taken in a consistent order, or one lock for the whole simulation step). There is no randomness. Time enters only through `now`.

## A7. Original hardcoded thing → how it is configurable here

| Original | Here |
| --- | --- |
| `DegradationFactor = 0.8` | `Config.DecayFactor`, default `0.8` |
| `MinSpreadConfidence = 0.2` | `Config.MinConfidence`, default `0.2` |
| `Spreadable` floor `0.3` in the store | `Config.MinSourceConfidence`, default `0.3` |
| Copies marked with `SourceHeard` | `Config.ReceivedKind`, default `"relayed"` |
| Global `spreadingFilters`, `RegisterSpreadingFilter`, `init()` | Per-`Spreader` `WithFilter`; no globals |
| `NeverSpreadSecretFilter` as a registered filter | Intrinsic `Private` exclusion |
| Built-in filter naming three groups of one setting and two fact types | Not shipped; caller-written or data-built condition (examples) |
| `EnemyBenefitFilter`, disposition ≤ `-50` | Not shipped; caller condition, threshold is an `args` value |
| `WillTellAbout(..., world)` with an ambient world reader | Filters get an `Encounter`; world access via the caller's closure |
| Spread to "other NPCs in the same room" | Caller passes the listener slice |
| No hop limit, no echo protection | `MaxHops` (default unlimited), `SkipBackToSource` (default on) |
| Confidence formula fixed | `Config.DecayFactor` plus the optional context-aware `Transform` hook (sees teller, listener, the teller's fact and the tick) |
| Nondeterministic map iteration | Candidates by confidence then ID; listeners in caller order |

## A8. Worked examples

### Spaceport trading hub (exercises `skip_back_to_source`, filters)

```go
type Crew struct { id string; kb *knowledge.Store; Guild string; Trust map[string]int }
func (c *Crew) ID() string                  { return c.id }
func (c *Crew) Knowledge() *knowledge.Store { return c.kb }

neverToHaulers := spread.Typed(func(e spread.TypedEncounter[*Crew]) bool { // customs staff never pass smuggling intel to the cargo guild
    t, l := e.Teller, e.Listener
    smuggling := e.Fact.Type == "captain_is_smuggler" || e.Fact.Type == "ship_flagged"
    return !(t.Guild == "customs" && l.Guild == "haulers" && smuggling)
})
notIfDistrusted := spread.Typed(func(e spread.TypedEncounter[*Crew]) bool { // nobody tells someone who distrusts the subject
    return e.Listener.Trust[e.Fact.Subject] > -50
})
cfg := spread.DefaultConfig()
cfg.ReceivedKind = "hearsay"
sp, _ := spread.New(cfg, spread.WithFilter(neverToHaulers), spread.WithFilter(notIfDistrusted))
res, _ := sp.Spread(ilse, []spread.Holder{oyelaran, vo}, now)
```

A fact Ilse holds at `0.9` arrives at `0.72` (`0.9 × 0.8`), `Source: "hearsay"`, `Prov{From: "dockhand-ilse", Hops: 1}`. Retold: `0.576`, `0.4608`, `0.3686`, then `0.2949` at hop 5. That fifth copy is delivered (above `0.2`) but its holder cannot retell it (below `0.3`), so at the defaults a rumour travels at most five hops. When Oyelaran later meets Ilse, the fact is not told back to her: with `skip_back_to_source` on its `Prov.From` is her ID; with it off she would receive a decayed copy of her own story (rejected by her store's merge, but still a wasted transfer).

### Office-gossip sim (exercises `max_hops`, `Transform`)

```go
cfg := spread.DefaultConfig()
cfg.DecayFactor = 0.9 // office gossip barely degrades
cfg.MaxHops = 4       // never past the fourth retelling
cfg.ReceivedKind = "grapevine"
sp, _ := spread.New(cfg, spread.WithFilter(neverTellBossAbout))
sp.Transform = func(_ spread.Encounter, f knowledge.Fact) knowledge.Fact { // embellishment: each retelling sounds a bit surer
    f.Confidence = math.Min(1, f.Confidence+0.05)
    return f
}
```

A `layoffs_planned` fact at `0.5` first-hand: hop 1 `0.5×0.9+0.05 = 0.5`, hop 2 `0.5`, and so on, hovering near `0.5` because decay and embellishment roughly cancel; at hop 4 its holder has `Hops == MaxHops` and cannot retell it. `neverTellBossAbout` is a caller filter: listener's role is `manager` and the fact type is one of `layoffs_planned`, `promotion_pending`.

### Harbour market (exercises a context-aware `Transform`: teller, listener, age)

```go
cfg := spread.DefaultConfig() // decay 0.8, min confidence 0.2
sp, _ := spread.New(cfg)
sp.Transform = func(e spread.Encounter, d knowledge.Fact) knowledge.Fact {
    if e.Teller.ID() == "gossip-bram" { // a boastful teller inflates it
        d.Confidence += 0.1
    }
    if e.Listener.ID() == "sceptic-ola" { // a sceptical listener discounts it
        d.Confidence /= 2
    }
    if e.Now-e.Fact.Observed > 50 { // stale news is told less confidently
        d.Confidence /= 2
    }
    return d
}
```

One hook, no per-holder spreaders. A `cargo_late` fact held at `0.8` and observed at tick `0` decays to `0.64` on delivery. Told by another trader to a neutral listener at tick `40` it arrives at `0.64` (age 40, no condition applies). Told by Bram at tick `40` it arrives at `0.74`. Told by Bram to Ola at tick `40`: `(0.64+0.1)/2 = 0.37`. Told at tick `90` (age 90) to a neutral listener it arrives at `0.32`; if the transform had instead returned `0.15` the delivery would be skipped, because the `MinConfidence` check runs after the transform. `e.Fact.Confidence` is still `0.8` (the teller's version before decay), so a transform can also compare the two.

---

# Part B: Inference

## B1. Purpose

Derive new facts from combinations of facts a holder already has: "if I know X about someone and Y about them, I conclude Z". Rules are pure data (loadable from YAML/JSON). Derived facts are ordinary facts with a configurable source kind and provenance listing their premises.

## B2. Public API

```go
package infer

type Requirement struct {
    Type          string   `json:"type" yaml:"type"`                                          // required
    Subject       string   `json:"subject,omitempty" yaml:"subject,omitempty"`                // "" = the rule's bound subject; else a fixed ID
    MinConfidence *float64 `json:"min_confidence,omitempty" yaml:"min_confidence,omitempty"`  // nil = store query floor
    Sources       []string `json:"sources,omitempty" yaml:"sources,omitempty"`                // empty = any source kind
}

type Product struct {
    Type    string `json:"type" yaml:"type"`                           // required
    Subject string `json:"subject,omitempty" yaml:"subject,omitempty"` // "" = the bound subject; else a fixed ID
    TTL     int64  `json:"ttl,omitempty" yaml:"ttl,omitempty"`         // ticks; 0 = never expires
    Private bool   `json:"private,omitempty" yaml:"private,omitempty"`
    Payload any    `json:"payload,omitempty" yaml:"payload,omitempty"`
}

type Rule struct {
    ID         string        `json:"id" yaml:"id"`
    Requires   []Requirement `json:"requires" yaml:"requires"`
    Produces   Product       `json:"produces" yaml:"produces"`
    Confidence float64       `json:"confidence" yaml:"confidence"` // derived confidence, in (0,1]
}

type Config struct {
    MaxPasses   int    `json:"max_passes" yaml:"max_passes"`
    IDPrefix    string `json:"id_prefix" yaml:"id_prefix"`
    DerivedKind string `json:"derived_kind" yaml:"derived_kind"`
}
func DefaultConfig() Config
func (c Config) Validate() error

type Engine struct{ /* unexported */ }
type Option func(*Engine)
func New(cfg Config, rules []Rule, opts ...Option) (*Engine, error)

type Hooks struct {
    OnRun     func(Result, error)   // Result and the error Run returns
    OnRunDone func(time.Duration)   // after OnRun; only when WithClock was given
}
func WithHooks(h Hooks) Option                  // repeatable; each non-nil field chained independently, in the order given
func WithLogger(l *slog.Logger) Option          // nil ignored; default discards
func WithClock(now func() time.Time) Option     // enables OnRunDone; nil ignored; default none = no timing, no overhead
func (e *Engine) Run(store *knowledge.Store, now knowledge.Tick) (Result, error)

type Result struct {
    Derived []knowledge.AddResult // inserted or replaced, in the order produced
    Passes  int                   // passes executed (>= 1)
    // Truncated is true when Run stopped because it reached MaxPasses AND that
    // last pass still inserted or replaced something, so a further pass might have
    // derived more (or a rule cycle keeps replacing facts). Always false when
    // MaxPasses == 1 (chaining is off by design) and when a pass changed nothing.
    Truncated bool
}
```

| Field | Default | Meaning |
| --- | --- | --- |
| `MaxPasses` | `1` | Maximum passes per `Run`; `1` = one pass, no chaining (reference behaviour). `>1` lets derived facts feed later rules; `Run` stops early when a pass changes nothing. This bounds chaining and any rule cycles. |
| `IDPrefix` | `"inferred"` | Derived ID is `IDPrefix:ruleID:subject`. |
| `DerivedKind` | `"derived"` | Source kind stamped on derived facts; must be ranked by the caller. |

**Observability.** `OnRun` runs once at the end of every `Run`, including failed runs and the `ErrNilStore` call (zero `Result`), with the error `Run` returns; exhausting `MaxPasses` is not an error, but `Result.Truncated` reports it (precisely: `MaxPasses > 1`, the pass numbered `MaxPasses` ran, and it inserted or replaced at least one fact; a run that ends because a pass changed nothing, or that fails with a store error, is not truncated). Options chain in the order given; each non-nil `Hooks` field is chained independently. `WithClock(now)` enables timing: `now` is read at the start of `Run` and just before `OnRun`, and `OnRunDone(d)` runs after `OnRun` with `d` floored at 0, for every `Run` including the `ErrNilStore` call; with no clock it never runs and the clock is never read. `Result` carries no duration, so it stays deterministic. `WithLogger` logs a Debug summary per `Run` (passes, derived, truncated, and `by_rule`: a map from rule ID to the number of facts that rule inserted or replaced in this run, rules that derived nothing omitted), a Warn when `Run` returns an error, and a Warn when the run was truncated (passes, max_passes, derived). Per-rule counts are a log-only diagnostic (rule IDs are unbounded for metrics) and are not part of `Result`. Facts it derives also reach `knowledge.Hooks.OnAdd` on the store. Hooks are synchronous and read-only.

## B3. Behaviour: `Run(store, now)`

Rules are immutable after `New`. Each pass:

1. Snapshot the store's facts visible at `now` (unexpired; each requirement applies its own floor). All rules evaluate against this snapshot, so rule order within a pass does not matter.
2. For each rule in the order given:
   - **Matching.** A fact satisfies a requirement if its `Type` equals `Type`, confidence ≥ the requirement's floor, source kind is in `Sources` (if non-empty), and, for a fixed `Subject`, its subject equals it.
   - **Binding.** Requirements with empty `Subject` share one **bound subject** `s`: each must be satisfied by a fact with subject `s`. Fixed-subject requirements are checked independently of `s`.
   - **Candidates.** With no unbound requirement the only candidate is `Produces.Subject` (validation guarantees it is set). Otherwise candidates are the subjects of facts satisfying any one unbound requirement, deduplicated, ascending. The rule fires for each candidate satisfying every unbound and every fixed requirement.
   - **Producing.** `Type`, `Private`, `Payload` from the product; `Subject = Produces.Subject` if set, else `s`; `ID = IDPrefix:ruleID:subject`; `Source = DerivedKind`; `Confidence = Rule.Confidence`; `Observed = now`; `Expiry = now + TTL` if `TTL > 0`; `Prov = {Premises: sorted IDs of the best matching fact per requirement}` (best = highest confidence, ties ascending ID).
   - `store.Add(derived)`. Re-deriving an unchanged conclusion is a `Rejected` tie, so `Run` is idempotent.
3. If a pass inserted or replaced nothing, stop. Otherwise continue to `MaxPasses`.

A fixed-subject product with unbound premises yields the same ID for every candidate; the merge keeps the strongest. A derived fact does not track its premises: if one later expires, the derived fact stays until its own expiry. Use `TTL` if that matters.

Truncation example (office gossip, `MaxPasses = 2`): rules `a: late_report -> missed_deadline`, `b: missed_deadline -> team_stressed`, `c: team_stressed -> overtime_ban`. Pass 1 derives `missed_deadline`, pass 2 derives `team_stressed` and changes the store, and `MaxPasses` is reached, so `Result{Passes: 2, Truncated: true}`: `overtime_ban` is not derived until the next `Run` (or a higher `max_passes`). With `MaxPasses = 3` the third pass derives `overtime_ban`, still changed something, so `Truncated` is true again; `MaxPasses = 4` ends on a pass that changed nothing, `Truncated` false.

## B4. Error cases

`New` returns `ErrInvalidRule` (all problems joined) for: empty or duplicate rule ID; empty `Requires`; empty requirement or product `Type`; `Confidence` outside `(0,1]`; negative `TTL`; `MinConfidence` outside `[0,1]`; every requirement fixed-subject while `Produces.Subject` is empty. `ErrInvalidConfig` for `MaxPasses < 1` or empty `IDPrefix`/`DerivedKind`. `Run` with a nil store returns `ErrNilStore`; store errors during a pass (for example a derived ID colliding with a fact of another type) are returned joined.

## B5. Concurrency and determinism

`Engine` is immutable after `New`, but `Run` writes to the store, which is not safe for concurrent use; the caller serialises. Candidate subjects, rule order and premise choice are fully specified. No randomness; time enters via `now`.

## B6. Original hardcoded thing → how it is configurable here

| Original | Here |
| --- | --- |
| Package-level default engine + `RegisterInferenceRule` | `infer.New(cfg, rules)`; instance only, immutable |
| Derived facts forced to `SourceInferred` | `Config.DerivedKind`, default `"derived"` |
| Derived ID `inferred_<rule>_<subject>` | `IDPrefix:ruleID:subject`, prefix configurable |
| One pass only | `Config.MaxPasses`, default `1` (same) |
| `Requirement.Source *SourceType` (one enum value) | `Sources []string` |
| Unset confidence floor sees everything | `nil` = store query floor; explicit `0` allowed |
| Ad hoc fixed-subject precedence | Specified binding rules (B3) |
| Product expiry as an absolute time | `TTL` ticks relative to `now` |
| No payload, never private | `Payload`, `Private` on `Product` |
| Fact types mentioned by rules were game constants | Free strings in data |
| Rules registered by Go code at init | Plain Go values or the `inference:` document section |

## B7. Worked examples

### Spaceport trading hub

```yaml
inference:
  - id: flagged-and-seized-means-smuggler
    requires:
      - { type: ship_flagged, min_confidence: 0.4 }
      - { type: cargo_seized, min_confidence: 0.5, sources: [witnessed, customs_notice, hearsay] }
    produces: { type: captain_is_smuggler, ttl: 5000 }
    confidence: 0.7
```

A broker holding `ship_flagged` and `cargo_seized` about `freighter-aurelia` derives `captain_is_smuggler` about it at `0.7`, source `deduced` (if the caller set `derived_kind: deduced`), ID `inferred:flagged-and-seized-means-smuggler:freighter-aurelia`, premises in provenance, expiring `5000` ticks later. Running again changes nothing.

### Office-gossip sim (exercises `max_passes`)

```yaml
inference:
  - id: freeze-plus-late-meetings
    requires:
      - { type: hiring_freeze,      subject: company }
      - { type: late_meetings_seen, subject: company, min_confidence: 0.3 }
    produces: { type: layoffs_planned, subject: company }
    confidence: 0.4
  - id: layoffs-then-reorg
    requires: [ { type: layoffs_planned, subject: company, min_confidence: 0.35 } ]
    produces: { type: reorg_coming, subject: company }
    confidence: 0.5
```

With `infer.max_passes: 2` the first rule fires in pass 1 and the second in pass 2 (it needs the derived `layoffs_planned`). With the default `max_passes: 1` only the first fires; chaining is opt-in.
