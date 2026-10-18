# Spec: knowledge

Package: `knowledge` (standard library only). Status: approved with changes; this is the Phase 2 slice and is implemented.

## 1. Purpose

Per-holder storage of facts with confidence, source ranking, provenance and expiry. Defines how a duplicate claim merges with an existing one and how facts are queried. Fact types, source kinds and their ranks are entirely caller-defined.

## 2. Public API

```go
package knowledge

// Tick is the library's only notion of time. What one tick means is the caller's
// decision. Larger is later.
type Tick int64

// Fact is one claim held by one holder. Payload is shared by reference and must be
// treated as immutable; Prov.Premises is copied on the way in and out of a Store.
type Fact struct {
    ID            string     // identity; derived from Type, Subject, Discriminator when empty
    Type          string     // caller-defined; required
    Subject       string     // who/what it is about; may be empty
    Discriminator string     // optional extra identity part (which victim, which item, which event)
    Payload       any        // optional caller data; never inspected
    Confidence    float64    // belief in [0,1]; clamped on Add
    Source        string     // caller-defined source kind; required
    Prov          Provenance
    Observed      Tick       // when this holder acquired or last upgraded the fact
    FadedAt       Tick       // tick through which Fade has been applied; 0 = never faded; not part of identity
    Expiry        *Tick      // nil = never; expired when *Expiry <= now
    Private       bool       // never leaves the holder through spreading
}

type Provenance struct {
    From     string   // holder that told this holder; empty if first-hand
    Hops     int      // tellings between the origin and this holder; 0 = first-hand
    EventID  string   // caller's ID of the causing event, if any
    Premises []string // fact IDs an inference used; empty otherwise
}

type Query struct {
    Type          string   // "" = any
    Subject       string   // "" = any
    Sources       []string // empty = any source kind
    MinConfidence *float64 // nil = Config.QueryFloor
}

type Outcome int // Rejected, Inserted, Replaced; has String(): "rejected", "inserted", "replaced"

type AddResult struct {
    ID      string
    Outcome Outcome
    Evicted []string // IDs removed to honour Config.MaxFacts, in eviction order
}

type Decision int // Keep, Replace

// MergePolicy decides what happens when incoming has the same ID as existing.
// rank maps a source kind to its configured rank.
type MergePolicy func(existing, incoming Fact, rank func(source string) int) Decision

func RankThenConfidence(existing, incoming Fact, rank func(string) int) Decision

// Fader decides how a fact weakens with time. f is a copy of the whole fact (including
// Private); elapsed is the ticks since the fact was last settled, never negative. It
// returns the new confidence and whether to forget the fact outright. It runs inside
// Fade on the caller's goroutine: not safe for concurrent use against a shared store,
// and it must not call back into the store.
type Fader func(f Fact, now, elapsed Tick) (confidence float64, forget bool)

// FadeResult lists what one Fade call did, each slice in ascending ID order.
type FadeResult struct {
    Changed   []string // survived with a different confidence; forgotten IDs are not repeated here
    Forgotten []string // IDs removed from the store
    Invalid   []string // IDs whose Fader returned NaN: left unchanged and unsettled
    Examined  int      // unexpired facts the Fader was called on
}

type Store struct{ /* unexported */ }

func NewStore(cfg Config, opts ...Option) (*Store, error) // validates cfg
type Option func(*Store)
func WithMergePolicy(p MergePolicy) Option // default: RankThenConfidence
func WithFader(f Fader) Option             // replaces the config-built fader; nil is ignored; not safe for concurrent use against a shared store

func (s *Store) Add(f Fact) (AddResult, error)
func (s *Store) Get(id string) (Fact, bool)                   // raw: ignores floor and expiry
func (s *Store) Find(q Query, now Tick) []Fact                // unexpired, >= floor, ascending ID
func (s *Store) Prune(now Tick) int                           // deletes expired facts; returns count
func (s *Store) Fade(now Tick) FadeResult                     // weakens/forgets unexpired facts by the fader; see 3.5
func (s *Store) All() []Fact                                  // everything, ascending ID; for persistence/debug

var ErrInvalidFact, ErrIdentityMismatch, ErrInvalidConfig error
```

`Find`, `Get` and `All` return copies.

### Config

```go
type Config struct {
    SourceRanks  map[string]int `json:"source_ranks" yaml:"source_ranks"`
    UnrankedRank int            `json:"unranked_rank" yaml:"unranked_rank"`
    QueryFloor   float64        `json:"query_floor" yaml:"query_floor"`
    MaxFacts     int            `json:"max_facts" yaml:"max_facts"`
    Fade         FadeConfig     `json:"fade" yaml:"fade"`
}

// FadeConfig configures the built-in exponential half-life fader. All ticks are int64.
// Plain data; Fade, which uses it, is not safe for concurrent use against a shared store.
type FadeConfig struct {
    HalfLives       map[string]int64 `json:"half_lives" yaml:"half_lives"`               // ticks, keyed by Fact.Source
    DefaultHalfLife int64            `json:"default_half_life" yaml:"default_half_life"` // for sources absent from HalfLives
    ForgetBelow     float64          `json:"forget_below" yaml:"forget_below"`           // forget when faded confidence is below this
}
func DefaultConfig() Config   // fresh copy each call, including the map
func (c Config) Validate() error
```

| Field | Default | Meaning |
| --- | --- | --- |
| `SourceRanks` | `{"observed": 3, "relayed": 2, "derived": 1}` | Rank per source kind; higher wins merges; equal ranks allowed. The caller replaces the map wholesale with its own vocabulary. |
| `UnrankedRank` | `0` | Rank of a source kind absent from the map. Unknown kinds are accepted by the store; the aggregate config validation (see [config](config.md)) rejects unranked spread/inference kinds. |
| `QueryFloor` | `0.2` | Default minimum confidence for `Find`. Lower-confidence facts still exist and merge but are invisible to default queries. |
| `MaxFacts` | `0` | Cap on facts per store; `0` = unlimited. |
| `Fade.HalfLives` | empty map | Half-life in ticks per source kind for the built-in exponential fader. An entry of `0` means that source never fades, even when `DefaultHalfLife > 0`. `DefaultConfig()` returns a fresh map each call. |
| `Fade.DefaultHalfLife` | `0` | Half-life for source kinds absent from `HalfLives`; `0` = they never fade. |
| `Fade.ForgetBelow` | `0` | A fact whose faded confidence is below this is forgotten (removed). `0` forgets nothing by confidence. Only acts when a fader is in effect (see 3.5). |

The zero `Config{}` is valid and useful: no ranks (every kind shares `UnrankedRank`), no query floor (a zero `QueryFloor` shows everything, unlike the `0.2` default), no cap and no fading. `Validate` and `NewStore` accept it.

The three default source names exist only so a zero-configuration store works and so the spread/inference defaults (`relayed`, `derived`) have ranks.

## 3. Behaviour

### 3.1 Add

1. **Validate.** `Type` and `Source` non-empty; `Confidence` not NaN; no U+001F in `Type`, `Subject` or `Discriminator`. Otherwise `ErrInvalidFact`.
2. **Clamp** `Confidence` to `[0,1]`.
3. **Identity.** If `ID == ""`, `ID = Type + U+001F + Subject + U+001F + Discriminator`. Two tellings of the same claim about the same subject are the same fact and merge. `Type`, `Subject` and `Discriminator` must not contain U+001F (the separator, chosen because it cannot collide with ordinary text); `Add` rejects them with `ErrInvalidFact`. For distinct occurrences set `Discriminator` (e.g. an event ID) or supply your own `ID`.
4. **New ID** → insert, `Inserted`.
5. **Existing ID.** If `Type` or `Subject` differ, return `ErrIdentityMismatch` and change nothing. Otherwise ask the `MergePolicy`: `Keep` → `Rejected`; `Replace` → overwrite whole fact, `Replaced`.
6. **Enforce `MaxFacts`** (> 0). Only an insert can exceed the cap. While over, evict the weakest: lowest rank, then lowest confidence, then oldest `Observed`, then ascending ID. If the just-inserted fact is the weakest it is evicted: the result is `Rejected` and `Evicted` contains its ID.

### 3.2 Default merge policy

| Incoming vs existing | Decision |
| --- | --- |
| lower rank | `Keep` |
| higher rank | `Replace` |
| equal rank, higher confidence | `Replace` |
| equal rank, equal or lower confidence | `Keep` (ties keep existing, so re-adding the same fact is idempotent) |

A `Replace` overwrites the whole fact (confidence, source, provenance, observed, expiry, payload).

### 3.3 Expiry

Expired at `now` when `Expiry != nil && *Expiry <= now`. `Find` never returns expired facts. They stay until `Prune(now)` or eviction; `Get` and `All` include them. Adding an already-expired fact is allowed.

### 3.4 Queries

`Find` is a conjunction of `Type`, `Subject`, `Sources` (any of) and `confidence >= MinConfidence` (default `QueryFloor`) over unexpired facts, ascending by ID. Matching is exact and case-sensitive everywhere in the library; callers normalise.

`Get` and `All` are the raw view: they ignore the confidence floor and expiry. `Find` is the filtered view. The query floor is a query-level view, not a storage rule: facts below it are still stored, still merge, and are evicted by the ordinary weakest-first rule (lower rank, then lower confidence), with no special case for hidden facts.

### 3.5 Fade

`Fade(now)` is a caller-driven maintenance step: the library has no clock or scheduler, the caller calls it at whatever cadence it likes. Fading is opt-in.

1. **No fader, no-op.** The store's fader is the one from `WithFader`, otherwise the exponential fader built from `Config.Fade`. That built-in fader exists only if some `HalfLives` entry is positive or `DefaultHalfLife > 0`. With no fader, `Fade` changes nothing and returns an empty `FadeResult`, even when `ForgetBelow > 0`: forgetting is part of fading and is opt-in too. `WithFader(nil)` is ignored.
2. **Walk.** Visit facts in ascending ID order. Skip expired facts (`Expiry != nil && *Expiry <= now`): they are neither changed nor forgotten and stay until `Prune`. `Fade` never prunes and never evicts.
3. **Settle point.** A fact is settled at `max(Observed, FadedAt)`; `FadedAt == 0` means never faded, so `Observed` governs and a fact never decays for time before the store held it. `elapsed = max(0, now - settled)`: never negative, and the subtraction saturates at the maximum `int64` instead of wrapping.
4. **Call the fader** with a copy of the whole fact, `Private` included: private facts are the holder's own beliefs and fade like any other. A custom fader can exempt them. The built-in policy does not.
5. **Apply.** A NaN confidence leaves that fact unchanged and not settled (`FadedAt` untouched). Otherwise clamp to `[0,1]`. If the fader returned `forget`, or the clamped confidence is below `Config.Fade.ForgetBelow`, remove the fact and list it in `Forgotten`. Else store the confidence and set `FadedAt = max(FadedAt, now)`; list the ID in `Changed` only if the confidence differs from before. `Fade` changes only `Confidence` and `FadedAt`; `Observed`, `Source`, `Prov`, `Expiry`, `Payload`, `Private` and identity fields are untouched.
6. **Built-in curve.** For a half-life `h > 0` the new confidence is `confidence * 0.5^(elapsed/h)`; `h = 0` never fades. `h` is `HalfLives[Source]` when present (so a `0` entry exempts that source), else `DefaultHalfLife`. It is the only shipped curve; any other is a `Fader` via `WithFader`.
7. **Composition.** Because each call decays only the time since the fact was last settled, `Fade(100)` then `Fade(200)` gives the same built-in result as a single `Fade(200)`. A re-added fact replaces the whole fact, so it carries the incoming `FadedAt` (normally `0`).
8. **Diagnostics.** `Examined` counts every unexpired fact the fader was called on, NaN results included; `Invalid` lists the NaN ones in ascending ID order. `Examined == 0` on a non-empty store means no fader is wired up (for example a mistyped `half_lives` key or all-zero half-lives), which is otherwise indistinguishable from "nothing needed to change". A persistently non-empty `Invalid` means a buggy `Fader`.

`FadedAt` is not part of identity and `Add` does not validate it. `All()` returns it, so a persist-and-reload through `All()` and `Add` keeps fading progress. Faded confidence feeds the ordinary rules: `Find` applies the query floor to it, and `MaxFacts` eviction compares it, so a faded store evicts differently from an unfaded one.

## 4. Error cases

| Error | When |
| --- | --- |
| `ErrInvalidFact` | empty `Type` or `Source`, NaN `Confidence`, U+001F in `Type`/`Subject`/`Discriminator` |
| `ErrIdentityMismatch` | `Add` with an existing ID but different `Type`/`Subject` |
| `ErrInvalidConfig` | `QueryFloor` outside `[0,1]` or NaN, `MaxFacts < 0`, empty source name in `SourceRanks`, negative `Fade.HalfLives` value, negative `Fade.DefaultHalfLife`, empty source name in `Fade.HalfLives`, `Fade.ForgetBelow` outside `[0,1]` or NaN |

Errors wrap the sentinel with `%w`; check with `errors.Is`. `Validate` reports every problem, joined.

## 5. Concurrency and determinism

`Store` is **not safe for concurrent use**; wrap it with your own lock. `Find` and `All` sort the ID set on every call (O(n log n)); that is fine for NPC-sized stores, but do not use a store as a large database. No clocks, no RNG. All results are ordered by ascending fact ID; eviction order is fully specified. The store makes no I/O, so no `context.Context`.

`Fade` is not safe for concurrent use against a shared store, like every other `Store` method. The `Fader` is called synchronously on the caller's goroutine and must not call back into the same store. Panics from a caller-supplied `Fader` propagate. `Fade` is O(n log n) in store size (sorted ID walk), like `Find` and `All`. It is deterministic: facts are visited in ascending ID order and only `now`, stored fields and the injected fader are used, so identical inputs give identical `FadeResult` and `All()`. The built-in curve uses `math.Pow`, stable on one platform; compare with a small tolerance.

## 6. Original hardcoded thing → how it is configurable here

| Original | Here |
| --- | --- |
| Four-value `SourceType` enum | `Fact.Source` free string |
| `sourceStrength` switch (3, 3, 2, 1, default 0) | `Config.SourceRanks` + `UnrankedRank`; kinds can share a rank, as in the reference |
| 28 `KnowledgeType` constants with setting baked into the names | `Fact.Type` free string |
| Confidence clamp `[0,1]` | Kept: it defines the scale, not a tuning constant |
| Query floor `0.2` hardcoded | `Config.QueryFloor`, default `0.2`, per-query override |
| Merge: rank, then confidence, silent ties | `MergePolicy`, default `RankThenConfidence`, tie rule written down |
| Fact ID built from a calendar timestamp | Type + subject + discriminator; no calendar |
| `GameTime{Year..Minute}` + `ToUnix` | `Tick` (`int64`) |
| `*GameTime` expiry, component-wise addition | `Expiry *Tick`; plain tick arithmetic |
| `Object interface{}` + `objectKey` | `Payload any` + `Discriminator` |
| `Secret bool` | `Private bool` |
| Provenance = one enum | `Source` + `Provenance{From, Hops, EventID, Premises}` |
| Unbounded growth | `Prune(now)` plus optional `MaxFacts` with specified eviction |
| `Spreadable(now)` with floor `0.3` inside the store | Moved to the spreader; the store only stores and queries |
| `AllFacts` for persistence | `Store.All` |
| Confidence changed only when retold; no time-based weakening | `Store.Fade` with an injected `Fader`; built-in exponential policy from `Config.Fade`, off by default |

## 7. Worked examples

The first two are real `Example_` tests in `knowledge/example_test.go`; the fade example is `ExampleStore_Fade` in the same file.

### Spaceport trading hub

```go
cfg := knowledge.DefaultConfig()
cfg.SourceRanks = map[string]int{"witnessed": 3, "customs_notice": 3, "hearsay": 2, "deduced": 1}
store, _ := knowledge.NewStore(cfg)

r, _ := store.Add(knowledge.Fact{Type: "cargo_seized", Subject: "freighter-aurelia", Discriminator: "bay-4",
    Confidence: 0.6, Source: "hearsay", Observed: 1040, Prov: knowledge.Provenance{From: "dockhand-ilse", Hops: 1}})
// r.Outcome == Inserted. Seeing it first-hand (witnessed, 0.9) -> Replaced: rank 3 beats rank 2.
// Hearing it again (hearsay, 0.95) -> Rejected: lower rank loses whatever the confidence.
```

### Office-gossip sim

```go
cfg.SourceRanks = map[string]int{"announcement": 3, "overheard": 2, "grapevine": 2, "guess": 1}
cfg.MaxFacts = 2 // people forget: the weakest fact is evicted first
// grapevine 0.5 then grapevine 0.7: equal rank, higher confidence -> Replaced. grapevine 0.4 -> Rejected.
// With Expiry set, Find stops returning the fact at the expiry tick; Prune removes it.
```

### Harbour market: rumours fade

```go
cfg := knowledge.DefaultConfig()
cfg.SourceRanks = map[string]int{"witnessed": 3, "overheard": 2}
cfg.Fade = knowledge.FadeConfig{HalfLives: map[string]int64{"overheard": 100}, ForgetBelow: 0.15}
store, _ := knowledge.NewStore(cfg)

store.Add(knowledge.Fact{Type: "price_rumour", Subject: "salt", Confidence: 0.8, Source: "overheard", Observed: 0})
store.Add(knowledge.Fact{Type: "price_rumour", Subject: "rope", Confidence: 0.9, Source: "witnessed", Observed: 0})
// Fade(100): the overheard fact is 0.4 (one half-life), Changed lists it. "witnessed" has no half-life: unchanged.
// Fade(200): 0.2. Fade(300): 0.1 < ForgetBelow 0.15, so it is forgotten (Forgotten lists it, Get is false).
// Calling Fade(200) once on a fresh copy gives the same 0.2: FadedAt makes the steps compose.
// A custom curve goes through WithFader(func(f Fact, now, elapsed Tick) (float64, bool) {...}).
```
