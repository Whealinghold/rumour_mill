# rumour_mill: overview

Status: approved with changes (Phase 1 review); all packages implemented as of Phase 5. Go conventions: `doc/rules/architecture.md`, "Idiomatic Go".

## 1. Purpose and scope

`rumour_mill` is a Go library that models how information moves between simulated entities:

1. **Events** happen. Layered **rules** decide what each entity learns from them.
2. Each entity holds a **knowledge store** of **facts**, each with a confidence, a source kind, and provenance.
3. Entities **spread** facts to each other. Confidence **decays** on every hop and caller-supplied **policy** decides who may tell whom.
4. An **inference engine** derives new facts from combinations of existing ones.

The library is domain-free. It contains no fact types, source kinds, entity attributes, calendars, or tuning constants of any specific world. A spaceport trading hub, a noir detective sim, a colony sim and an office-gossip sim all use the same code with different config and different caller-registered conditions and actions.

It ships in-memory storage, generic combinators, a name catalog for declarative loading, and a JSON loader. It ships no setting-specific content.

## 2. Glossary

| Term | Meaning |
| --- | --- |
| Holder | Anything with a knowledge store and a stable string ID (usually an NPC; a notice-board works too). |
| Fact | One claim held by one holder: type, subject, confidence, source kind, provenance, optional expiry. |
| Fact type | Caller-defined string naming a kind of claim (e.g. `cargo_seized`). Never interpreted by the library. |
| Subject | The ID a fact is about. May be empty. |
| Source kind | Caller-defined string saying how the holder came to know (e.g. `witnessed`, `grapevine`). Ranked by config. |
| Rank | Integer strength of a source kind. Higher beats lower when the same fact arrives twice. |
| Confidence | Float in `[0,1]`: how strongly the holder believes the fact. |
| Provenance | Who told the holder, hops from the origin, the causing event, and the premises an inference used. |
| Tick | `knowledge.Tick` (an `int64`): the library's only notion of time. The caller decides what a tick is. |
| Context (`C`) | A caller-defined value handed to conditions and actions. Typically bundles the entity, the event and the world. |
| Event type | Caller-defined string naming the kind of event a rule reacts to. |
| Layer / Scope | An ordered tier of rules (caller-defined) / a key inside a layer (e.g. a group name). |
| Rule | Event type + optional condition + action + priority + stop mode. |
| Condition / Action | `func(C) bool` / `func(C) error`. Registered by name in a `Catalog` so data files can reference them. |
| Encounter | One teller, one listener, one fact, at one tick. Spreading filters judge encounters. |
| Inference rule | Pure data: premises and a produced fact. |

## 3. Package layout

```
github.com/Whealinghold/rumour_mill            package rumourmill: package doc only (+ leak-guard test)
.../knowledge   Tick, Fact, Store, MergePolicy, Config                      stdlib only
.../logic       Condition[C], Action[C], All/Any/Not/Sequence/If            stdlib only
.../rules       Layer, Rule, Registry, Config                               imports logic
.../spread      Holder, Encounter, Spreader, Config                         imports knowledge, logic
.../infer       Rule, Engine, Config                                        imports knowledge
.../declare     Args, Node, Catalog, knowledge ops, rule loading, Document,
                aggregate Config, Decoder                                   imports all of the above
.../internal/   helpers (not exported)
```

No import cycles; nothing imports `declare`. A caller who builds everything in Go never imports it.

Specs: [knowledge](knowledge.md), [rules](rules.md), [spreading and inference](spreading-and-inference.md), [declarative loading](declarative.md), [config](config.md).

Build order (all done): `knowledge`, `logic`+`rules`, `spread`+`infer`, `declare`.

## 4. Extension points

Everything domain-specific enters through one of these. Nothing else is extensible, by design.

| Extension point | Type | Supplied by | Used for |
| --- | --- | --- | --- |
| Fact type, source kind, event type, holder/subject IDs | `string` | caller data | all vocabulary |
| Source ranking | `knowledge.Config.SourceRanks` | config | which source wins a merge |
| Merge policy | `knowledge.MergePolicy` via `knowledge.WithMergePolicy` | option | how a duplicate fact combines |
| Context type | `C` type parameter of `logic`/`rules`/`declare.Catalog` | caller | the host world: entity, event, world |
| Layers | `rules.Layer[C]` | caller | how many tiers and how a context maps to scope keys |
| Named conditions/actions | `declare.Catalog[C]` | caller | everything a rule can test or do |
| Spreading policy | `logic.Condition[spread.Encounter]` via `spread.WithFilter` | caller | who may tell whom what |
| Rumour distortion | `Spreader.Transform` func field | caller | optionally alter a fact in transit (the only hook of its kind) |
| Inference rules | `[]infer.Rule` | config data | derivations |
| Time | `knowledge.Tick` parameter on every time-sensitive call | caller | expiry, observation time |

The library never calls `time.Now`, never uses a global RNG, and never registers anything in `init()`. It currently uses no randomness at all; if a future feature needs it, an RNG will be injected.

## 5. Cross-cutting guarantees

- **Determinism.** Same inputs, config and tick values give identical results. All iteration the library controls is ordered (facts by ID; rules by layer, priority, scope order, registration order; listeners in the order given).
- **Errors are values.** Constructors and mutators that can fail return errors wrapping a package sentinel (`errors.Is`). Library code does not panic on bad input. A panic inside a caller-supplied condition/action/filter/transform propagates.
- **Concurrency.** Stated per type. Summary: `knowledge.Store`, `spread.Spreader` and `infer.Engine` are not safe for concurrent use against shared stores (wrap with your own lock); `rules.Registry` and `declare.Catalog` are safe for concurrent use.
- **No global state.** Every registry is an instance.

## 6. Out of scope (deliberately)

Per architecture rules: game schedulers, database persistence, dialogue, behaviour gates tied to a game, combat, economy. Additionally:

- **Event delivery.** No event bus or subscriptions. The caller decides which entities an event reaches and calls `Registry.Process` for each.
- **Audience selection.** Who is in earshot of whom is decided by the caller, who passes a listener slice.
- **World model.** No rooms, zones, groups, relationships, dispositions, players. The caller's context type and registered conditions expose whatever they need.
- **Rumour mutation beyond the single `Transform` hook.** No built-in distortion strategies.
- **Belief revision.** Two facts that contradict each other are two facts. The library does not detect or resolve contradictions.
- **Negation / absence in inference.** Inference rules only test for the presence of facts.
- **Pluggable storage.** A storage interface is introduced when a second backend is actually needed (architecture, "Idiomatic Go" rule 3); until then `Store` is concrete and in-memory. `Store.All` is the persistence seam.
- **YAML parsing.** JSON uses the standard library. YAML is supported through a caller-supplied decoder; no YAML dependency.

## 7. Domain-fit check

| Setting | Holders | Layers | Typical fact types | Typical sources |
| --- | --- | --- | --- | --- |
| Spaceport trading hub | dockhands, brokers, inspectors | station, guild, individual | `cargo_seized`, `ship_flagged`, `fuel_price_spike` | `witnessed`, `customs_notice`, `hearsay`, `deduced` |
| Office-gossip sim | employees | company, department, person | `layoffs_planned`, `promotion_pending` | `announcement`, `overheard`, `grapevine`, `guess` |
| Noir detective | witnesses, informants | city, precinct, person | `alibi_false`, `owes_money` | `testimony`, `tip`, `rumour`, `hunch` |
| Colony sim | colonists | colony, crew, person | `water_leak`, `ration_cut_coming` | `inspected`, `radio`, `word_of_mouth`, `suspicion` |

Full worked examples for the first two appear in every spec.

## 8. Cuts after review

Removed from the Phase 1 draft: probabilistic spreading (`share_chance`) and the RNG with it, `max_facts_per_encounter`, the `scaled` inference confidence mode, the `Decayer` hook (the single `Transform` hook covers custom degradation), `KeyFunc` (a `Discriminator` or explicit `ID` covers identity), `Has`/`Remove`/`Len`/`Rank` on the store, the `Backend` interface (deferred, see section 6), and the `core` package (`Tick` lives in `knowledge`). Kept tunables, each exercised by an example: `max_facts` (office store), `max_hops` (office spread), `skip_back_to_source` (spaceport spread), `max_passes` (office inference).
