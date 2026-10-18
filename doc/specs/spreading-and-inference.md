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

type Spreader struct {
    // Transform, if non-nil, receives the copy about to be delivered (decayed
    // confidence, new source and provenance already applied) and returns what is
    // actually delivered. It may change Confidence, Payload and Expiry. ID, Type,
    // Subject, Discriminator, Source, Prov, Observed and Private are restored
    // afterwards, so a transform cannot change what a fact is or forge its origin.
    Transform func(knowledge.Fact) knowledge.Fact
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
}
type Result struct{ Transfers []Transfer }
```

Filters are ordinary `logic.Condition`s over `Encounter`, written in Go or built from data (see [declarative](declarative.md)), and composable with `logic.All/Any/Not`. A filter needing the caller's concrete types type-asserts `e.Teller`/`e.Listener`.

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

1. If `teller.Knowledge()` is nil, return an empty result.
2. Candidate facts: `Find` on the teller's store with `MinConfidence = MinSourceConfidence` at `now` (unexpired); drop `Private` facts; drop facts with `MaxHops > 0 && Prov.Hops >= MaxHops`. Privacy is intrinsic and cannot be disabled by filters or config. Order: confidence descending, then ascending ID.
3. For each listener in the order given (skip one whose ID equals the teller's or whose `Knowledge()` is nil), for each candidate:
   1. If `SkipBackToSource` and `Prov.From == listener.ID()`, skip.
   2. Run the filters on the `Encounter`, in the order added, short-circuiting; any `false` skips.
   3. Build the copy: same `ID`, `Type`, `Subject`, `Discriminator`, `Payload`, `Expiry`; `Confidence = fact.Confidence × DecayFactor`; `Source = ReceivedKind`; `Observed = now`; `Prov = {From: teller.ID(), Hops: fact.Prov.Hops+1, EventID: fact.Prov.EventID}` (premises not forwarded); `Private = false`.
   4. Apply `Transform` if set, restore the fixed fields (see A2), clamp confidence to `[0,1]`.
   5. If confidence `< MinConfidence`, skip.
   6. `listener.Knowledge().Add(copy)`; record a `Transfer` with the store's outcome. Normal merge rules apply, so a listener holding a better-ranked or stronger version is unaffected (`Rejected`).
4. The teller's store is never modified.

The teller's facts are read once up front; listener stores are written one at a time. Decay is per hop, not per time.

## A5. Error cases

`ErrInvalidConfig` from `New`: `DecayFactor` outside `(0,1]`; `MinConfidence` or `MinSourceConfidence` outside `[0,1]`; `MaxHops < 0`; empty `ReceivedKind`. `Spread` returns store errors (joined), for example `knowledge.ErrIdentityMismatch`; listeners already processed keep their facts (not transactional). Nil listeners, the teller itself and holders without a store are skipped. A nil filter is ignored. A transform that returns a NaN confidence drops that delivery. Panics from caller filters/transform propagate.

## A6. Concurrency and determinism

`Spreader` holds no mutable state. It is **not safe for concurrent use against shared stores**, because `knowledge.Store` is not; the caller serialises access (for example one lock per holder, taken in a consistent order, or one lock for the whole simulation step). There is no randomness. Time enters only through `now`.

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
| Confidence formula fixed | `DecayFactor` plus the single optional `Transform` hook |
| Nondeterministic map iteration | Candidates by confidence then ID; listeners in caller order |

## A8. Worked examples

### Spaceport trading hub (exercises `skip_back_to_source`, filters)

```go
type Crew struct { id string; kb *knowledge.Store; Guild string; Trust map[string]int }
func (c *Crew) ID() string                  { return c.id }
func (c *Crew) Knowledge() *knowledge.Store { return c.kb }

neverToHaulers := func(e spread.Encounter) bool { // customs staff never pass smuggling intel to the cargo guild
    t, l := e.Teller.(*Crew), e.Listener.(*Crew)
    smuggling := e.Fact.Type == "captain_is_smuggler" || e.Fact.Type == "ship_flagged"
    return !(t.Guild == "customs" && l.Guild == "haulers" && smuggling)
}
notIfDistrusted := func(e spread.Encounter) bool { // nobody tells someone who distrusts the subject
    return e.Listener.(*Crew).Trust[e.Fact.Subject] > -50
}
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
sp.Transform = func(f knowledge.Fact) knowledge.Fact { // embellishment: each retelling sounds a bit surer
    f.Confidence = math.Min(1, f.Confidence+0.05)
    return f
}
```

A `layoffs_planned` fact at `0.5` first-hand: hop 1 `0.5×0.9+0.05 = 0.5`, hop 2 `0.5`, and so on, hovering near `0.5` because decay and embellishment roughly cancel; at hop 4 its holder has `Hops == MaxHops` and cannot retell it. `neverTellBossAbout` is a caller filter: listener's role is `manager` and the fact type is one of `layoffs_planned`, `promotion_pending`.

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
func New(cfg Config, rules []Rule) (*Engine, error)
func (e *Engine) Run(store *knowledge.Store, now knowledge.Tick) (Result, error)

type Result struct {
    Derived []knowledge.AddResult // inserted or replaced, in the order produced
    Passes  int                   // passes executed (>= 1)
}
```

| Field | Default | Meaning |
| --- | --- | --- |
| `MaxPasses` | `1` | Maximum passes per `Run`; `1` = one pass, no chaining (reference behaviour). `>1` lets derived facts feed later rules; `Run` stops early when a pass changes nothing. This bounds chaining and any rule cycles. |
| `IDPrefix` | `"inferred"` | Derived ID is `IDPrefix:ruleID:subject`. |
| `DerivedKind` | `"derived"` | Source kind stamped on derived facts; must be ranked by the caller. |

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
