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

type Store struct{ /* unexported */ }

func NewStore(cfg Config, opts ...Option) (*Store, error) // validates cfg
type Option func(*Store)
func WithMergePolicy(p MergePolicy) Option // default: RankThenConfidence

func (s *Store) Add(f Fact) (AddResult, error)
func (s *Store) Get(id string) (Fact, bool)                   // raw: ignores floor and expiry
func (s *Store) Find(q Query, now Tick) []Fact                // unexpired, >= floor, ascending ID
func (s *Store) Prune(now Tick) int                           // deletes expired facts; returns count
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

The zero `Config{}` is valid and useful: no ranks (every kind shares `UnrankedRank`), no query floor (a zero `QueryFloor` shows everything, unlike the `0.2` default) and no cap. `Validate` and `NewStore` accept it.

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

## 4. Error cases

| Error | When |
| --- | --- |
| `ErrInvalidFact` | empty `Type` or `Source`, NaN `Confidence`, U+001F in `Type`/`Subject`/`Discriminator` |
| `ErrIdentityMismatch` | `Add` with an existing ID but different `Type`/`Subject` |
| `ErrInvalidConfig` | `QueryFloor` outside `[0,1]` or NaN, `MaxFacts < 0`, empty source name in `SourceRanks` |

Errors wrap the sentinel with `%w`; check with `errors.Is`. `Validate` reports every problem, joined.

## 5. Concurrency and determinism

`Store` is **not safe for concurrent use**; wrap it with your own lock. `Find` and `All` sort the ID set on every call (O(n log n)); that is fine for NPC-sized stores, but do not use a store as a large database. No clocks, no RNG. All results are ordered by ascending fact ID; eviction order is fully specified. The store makes no I/O, so no `context.Context`.

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

## 7. Worked examples

Both are real `Example_` tests in `knowledge/example_test.go`.

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
