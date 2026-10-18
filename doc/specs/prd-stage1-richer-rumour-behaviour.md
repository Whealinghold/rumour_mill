# Stage 1: richer rumour behaviour PRD

Status: draft · Owner: Andy Beak · Last updated: 2026-10-18

Roadmap source: `doc/roadmap.md`, Stage 1. Amends (does not replace) [spreading-and-inference](spreading-and-inference.md) Part A and [knowledge](knowledge.md). Conventions followed: `doc/rules/specs.md` (spec sections), `doc/rules/architecture.md` (no domain vocabulary, no magic numbers, abstract time, injected policy), `doc/rules/testing.md` (table-driven, seeded/fake tick, `Example_` docs, `-race`, `go vet`). There is no test-pyramid doc beyond `testing.md`; levels used below are **unit** (table-driven, one package), **example** (`Example_` test, doubles as docs) and **integration** (two or more packages together). There is no UI surface.

## Problem & Goal

**Who.** The *consumer* is a game or simulation developer wiring the library. The *end user* is the player or observer of that simulation, who sees NPCs that tell stories differently depending on who is talking and to whom, and who sees old rumours die out.

**Today.**
1. `Spreader.Transform` receives only the delivered copy of a fact (`func(knowledge.Fact) knowledge.Fact`). It cannot see the teller, the listener or the current tick, so a consumer cannot express "a boastful teller inflates it", "a sceptical listener discounts it" or "stale news is told less confidently". The only workaround is a per-teller `Spreader` or a closure over mutable state, which breaks the library's "one spreader, many holders" model and hides the dependency.
2. Confidence only ever changes on a hop. A rumour nobody retells sits in every holder's store at full strength until its hard `Expiry`. There is no way to say "this belief weakens as time passes" or "after a while they forget it", so simulations either keep stale beliefs forever or build their own sweep over `Store.All()` plus `Add`, which cannot lower confidence (merge never accepts a weaker same-rank claim) and so needs unsupported tricks.

**Goal.** Rumours change in transit according to who and when (G1), and held beliefs weaken and are forgotten with the passage of caller-defined time (G2), with no clock in the library and no new domain vocabulary.

**Success measures** (all checkable in repo tests):
- A consumer expresses teller-dependent, listener-dependent and age-dependent distortion using only the one existing hook and the data it is handed (Example tests for each, no per-holder spreaders).
- A consumer ages a store by calling one method per tick (or per day) and observes confidence fall and facts disappear, with results identical for the same inputs (determinism test).
- Zero behaviour change for a consumer who sets no `Transform` and no fade config (existing tests pass unmodified except the `Transform` signature in the one spread example).

## Scope

### In scope
- Give `Spreader.Transform` the encounter (teller, listener, teller's fact, tick) as well as the delivered copy.
- A caller-driven `Store` maintenance step (`Fade`) that lowers confidence by an injected policy and forgets facts, run by the caller at whatever cadence it likes.
- An optional, configurable built-in policy (exponential half-life) so common cases need no Go code, loadable from data via the existing `knowledge` config section.
- Spec, README, example and CHANGELOG updates in the same change.

### Out of scope
- Any clock, scheduler or automatic fading; the caller calls `Fade`.
- Declarative (data-file) transforms. The hook stays a Go function.
- A transform combinator (`Chain`), a transform veto/"don't deliver" return, or changes to the filter signature.
- A new origin-time field on `Provenance`; age is derived from `Fact.Observed` (see D5).
- Fading derived by `infer`, per-type fade curves, non-exponential built-in curves (a custom `Fader` covers them).
- Contradictions, absence rules, persistence, indexing (later stages).
- Event delivery, audience selection, schedulers (permanently out of scope, `overview.md`).

### Priority
| Unit | Priority | End-user impact if cut |
| --- | --- | --- |
| U1 Context-aware `Transform` | Must | NPCs cannot tell stories differently per teller/listener/age |
| U2 `Store.Fade` + `Fader` hook | Must | Stale beliefs never weaken; the simulation feels static |
| U4 Docs, examples, spec updates | Must | Consumers cannot discover or trust the features (architecture rule 10, specs rule 5) |
| U3 Built-in exponential policy via config | Should | Every consumer re-implements the common decay curve in Go; data-only setups impossible |

## Requirements

Each requirement is one behaviour. "Consumer" means the developer calling the library.

### U1: context-aware distortion
- **R1.** `Spreader.Transform` is called with the `Encounter` for the delivery (same value the filters judged: teller, listener, the teller's fact before decay, current tick) and the delivered copy, and returns the fact to deliver.
- **R2.** `Encounter.Fact` as seen by `Transform` is the teller's version before decay, so a transform can compare it with the delivered copy.
- **R3.** `Transform` is invoked once per delivery that passed the filters, after the copy is built and before the `MinConfidence` check, in the same position as today.
- **R4.** After `Transform` returns, `ID`, `Type`, `Subject`, `Discriminator`, `Source`, `Prov`, `Observed` and `Private` are restored and confidence is clamped to `[0,1]`, as today. A NaN confidence drops the delivery, as today.
- **R5.** A nil `Transform` behaves exactly as today.
- **R6.** `Transform` is a named exported type (`spread.Transform`) so consumers can store, pass and wrap transforms.
- **R7.** The teller's store and the `Encounter` handed to `Transform` are not modified by the spreader on the transform's behalf. Transforms must treat `Encounter` and its `Fact.Prov.Premises` as read-only; this is documented, not enforced.

### U2: fading hook
- **R8.** `Store.Fade(now Tick) FadeResult` applies the store's `Fader` to every unexpired fact in ascending ID order and applies the results.
- **R9.** A `Fader` is `func(f Fact, now, elapsed Tick) (confidence float64, forget bool)`. `f` is a copy of the whole fact, including `Private`. `elapsed` is the number of ticks since the fact was last settled (see R10), never negative.
- **R10.** A fact is settled at `max(Observed, FadedAt)`. `Fact` gains `FadedAt Tick`: the tick through which fading has been applied; its zero value means "never faded" so `Observed` governs. After `Fade(now)` every unexpired, surviving fact the fader did not reject with NaN (R13) has `FadedAt = max(FadedAt, now)`. `elapsed = max(0, now - settled)`.
- **R11.** Calling `Fade` at ticks 100 then 200 yields the same built-in result as calling it once at 200 (the exponential policy composes), and a fact never decays for time before the store held it.
- **R12.** If the `Fader` returns `forget == true`, or a confidence below `Config.Fade.ForgetBelow` after clamping, the fact is removed from the store.
- **R13.** Otherwise the returned confidence is clamped to `[0,1]` and stored. A NaN return leaves that fact unchanged and not settled.
- **R14.** `Fade` changes only `Confidence` and `FadedAt`. `Observed`, `Source`, `Prov`, `Expiry`, `Payload`, `Private` and identity fields are untouched.
- **R15.** `Fade` skips expired facts (they remain for `Prune`); it never prunes and never evicts.
- **R16.** `FadeResult` lists `Changed` (IDs that survived with a different confidence; forgotten IDs are not repeated here) and `Forgotten` (IDs removed), each in ascending ID order, so the consumer can emit its own events or logs.
- **R17.** With no configured half-lives and no `WithFader` option, `Fade` changes nothing and returns an empty result (fading is opt-in).
- **R18.** A `Fader` supplied with `WithFader(f)` replaces the config-built one. `WithFader(nil)` is ignored, matching how a nil filter is ignored.
- **R19.** The `Fader` is called synchronously on the caller's goroutine and must not call back into the same store; this is documented.

### U3: built-in policy
- **R20.** `FadeConfig` has fields `HalfLives map[string]int64` (ticks, keyed by `Fact.Source`), `DefaultHalfLife int64` (ticks, used for sources absent from the map) and `ForgetBelow float64`.
- **R21.** For a half-life `h > 0` the built-in fader returns `confidence * 0.5^(elapsed/h)`. A half-life of `0` (or no entry and `DefaultHalfLife == 0`) means that source never fades.
- **R22.** Defaults: `HalfLives` empty, `DefaultHalfLife` 0, `ForgetBelow` 0, so a default store does not fade and nothing is forgotten by confidence.
- **R23.** `FadeConfig` lives at `knowledge.Config.Fade`, so it loads from the `knowledge:` section of a data file as `fade: {half_lives: {...}, default_half_life: N, forget_below: X}` with no change to `declare`.
- **R24.** `Config.Validate` rejects (joined, `ErrInvalidConfig`): a negative half-life, a negative `DefaultHalfLife`, an empty source key in `HalfLives`, `ForgetBelow` outside `[0,1]` or NaN.
- **R25.** `DefaultConfig()` returns a fresh copy of the `HalfLives` map each call (no shared state), and the zero `Config{}` remains valid.
- **R26.** The exponential built-in is the only shipped curve; any other curve is a `Fader` passed via `WithFader`.

### U4: documentation
- **R27.** `spreading-and-inference.md` (its sections A2, A4, A7, A8) and `knowledge.md` (API, config table, behaviour, errors, examples) are updated in the same change, `CHANGELOG.md` notes the breaking `Transform` signature and the new `Fade`, and the README quick start shows one transform and one fade call in a neutral domain.
- **R28.** Every new exported identifier has a doc comment stating concurrency (`Fade` and `Spread` are not safe for concurrent use against a shared store) and the `docs_test.go` and `TestNoDomainVocabulary` guards pass.

## Non-Functional Requirements

### Performance, security, reliability, scale
- `Fade` is O(n log n) in store size (sorted ID walk), the same as `Find`/`All`. Acceptable for NPC-sized stores; no benchmark required until Stage 4. `Transform` adds no allocation beyond passing the already-built `Encounter` by value.
- No panics on bad input in library code. Panics from caller-supplied `Transform`/`Fader` propagate, as filters do today.
- Determinism: `Fade` visits facts in ascending ID order and uses only `now`, stored fields and the injected fader; same inputs give identical results. `math.Pow` results are stable on one platform; tests compare with a small tolerance and document it.
- No new dependency (standard library `math` only).

### Privacy / compliance
N/A. The library stores no personal data; payloads are opaque to it.

### Observability
The library has no logging or metrics. `FadeResult` (changed and forgotten IDs) and the existing `Spread` `Result.Transfers` are the observability surface; the consumer logs from them.

### Rollout & release safety
- Breaking change: the type of `Spreader.Transform` changes. The library is `v0.x` and the field is the single hook; the migration is a one-line wrapper (`func(_ spread.Encounter, d knowledge.Fact) knowledge.Fact { return old(d) }`), recorded in `CHANGELOG.md`. Because it is a plain exported field assignment, the compiler flags every affected call site.
- Additive and default-off otherwise: new `Fact.FadedAt` zero value, `Config.Fade` zero value and `Fade` with no policy are no-ops; existing data files without `fade:` load unchanged (strict decoding still rejects typos).
- `All()` now returns `FadedAt`; a consumer persisting facts by copying `Fact` keeps it automatically. A consumer with a hand-written serialiser must add the field (CHANGELOG note).
- Rollback: revert the change; no data migration (an unknown `fade` key in a data file would then be rejected loudly).

## UX & Design

N/A: no user-facing surface. The equivalent concern, **developer experience**, is handled by usage-first design (architecture "Idiomatic Go" rule 1): the `Example_` tests in U4 are written first and the API adjusted until the call sites read well. Consistency with existing conventions: option-injected policy (`WithFader` mirrors `WithMergePolicy`), results as ID lists (mirrors `AddResult.Evicted`), config struct with `DefaultConfig`/`Validate`, tick as `int64`.

## Stakeholders & Sign-off / Acceptance Criteria

- Owner and approver: Andy Beak (spec approval per `doc/rules/specs.md` rule 1).
- Reconciliation of shared types with other stages: lead agent (pane wK:p1).
- Done when: every requirement above has a passing test or example; `go test ./...`, `go test -race ./...`, `go vet ./...` are clean; the leak guard and `docs_test.go` pass; the two specs, README and CHANGELOG are updated.

Requirement-to-test traceability is in "Testing / Key Scenarios" (scenario IDs S1...S14 cite requirements).

## Phases / Units

| Unit | Depends on | Risk | Decisions (see Decisions section) |
| --- | --- | --- | --- |
| U1 Context-aware `Transform` (R1-R7) | none | Low-medium: breaking field type; mitigated by compiler-flagged one-line migration | Decided: use the existing `Encounter` (roadmap says "teller, listener and current tick"). Decided: replace rather than add a second hook (D1) |
| U2 `Store.Fade` + `Fader` (R8-R19) | none (touches `knowledge.Fact`) | Medium: new `FadedAt` semantics, composition across irregular call cadence | Decided: per-fact `FadedAt` rather than hidden store state (D2); skip expired (D4) |
| U3 Built-in exponential policy (R20-R26) | U2 | Low | Decided: exponential, per source kind, opt-in (D3); nested under `knowledge.Config` (D6) |
| U4 Docs/examples/spec updates (R27-R28) | U1-U3 | Low | Decided (specs rule 5) |

Suggested order: U1 and U2 in parallel, then U3, then U4 (examples are drafted first per usage-first rule and finalised last).

Per-unit acceptance: U1 = S1-S5 pass; U2 = S6-S11 pass; U3 = S12-S13 pass; U4 = S14 passes and docs updated.

### Decisions (confirmed by the lead, 2026-10-18)
- **D1 (was A1).** Decided: replace the `Transform` field's type with `spread.Transform func(e Encounter, delivered knowledge.Fact) knowledge.Fact` instead of adding a second field. Reason: one hook (roadmap goal "without adding more hooks"), smallest surface.
- **D2 (was A2).** Decided: track fade progress per fact in `Fact.FadedAt`. Alternative: one hidden last-fade tick in `Store`, rejected because it is lost on reload via `All()`/`Add` (double decay), and wrong for facts added between runs.
- **D3 (was A3).** Decided: ship only exponential half-life decay, keyed by source kind, default off.
- **D4 (was A4).** Decided: `Fade` skips expired facts and does not prune; the consumer calls `Prune` then `Fade` (or the reverse) as it likes.
- **D5 (was A5).** Decided: age for a transform is `e.Now - e.Fact.Observed` (time since the teller acquired or last upgraded it), not time since the original event. No `Provenance` field is added; a consumer needing event time stores it in `Payload` or `Discriminator`.
- **D6 (was A6).** Decided: `FadeConfig` nests inside `knowledge.Config`.
- **D7 (was Q7, extended).** Decided: no `spread.Chain` and no transform veto return; consumers compose plain funcs and veto through filters.
- **D8 (was N1/A7).** Decided: `Fade` fades `Private` facts by default (they are the holder's own beliefs). The `Fader` receives the whole `Fact` including `Private`, so a consumer can exempt them in a custom `Fader`; the built-in policy does not exempt them.

## Cross-stage interfaces

Assumed interfaces for lead reconciliation (types other stages also touch):

- **`Spreader.Transform`.** Becomes `type Transform func(e Encounter, delivered knowledge.Fact) knowledge.Fact` and the field `Transform Transform`. `Encounter` is **unchanged** (`Teller, Listener Holder; Fact knowledge.Fact; Now knowledge.Tick`); the same value goes to filters and the transform. The invariant that `ID, Type, Subject, Discriminator, Source, Prov, Observed, Private` are restored after the call stays, and that list is exactly as written here: a later `Fact.Roles` (Stage 5) is not restored, so `Transform` may alter it, and Stage 2's `spread.TypedTransform` is a thin twin over this exact signature. There is no `spread.Chain` and no veto return (decided): consumers compose plain funcs and veto through filters. If another stage adds fields to `Encounter`, additive is fine; if another stage changes the Transform signature, it must supersede this one, not coexist.
- **Spreader filters.** No change. Filters remain `logic.Condition[Encounter]` via `WithFilter`. A Stage 2 "typed filter helper" that adapts `Encounter` to a caller's holder types can be reused for `Transform` by the same adapter shape, but this PRD does not require or provide it.
- **`declare.Catalog` / `declare.Config`.** No change required. `Transform` is not declarative. The fade policy loads through the existing `knowledge:` section (`knowledge.Config.Fade`); `declare.Config`, `Document` and `Catalog` are untouched. Strict decoding means data files gain one new valid key path, `knowledge.fade.{half_lives,default_half_life,forget_below}`.
- **`knowledge.Config`.** Gains one field: `Fade FadeConfig` (`json:"fade" yaml:"fade"`) with `HalfLives map[string]int64`, `DefaultHalfLife int64`, `ForgetBelow float64`. `DefaultConfig()` deep-copies `HalfLives`. Zero `Config{}` stays valid. `Validate` gains four checks (R24). Any other stage adding `knowledge.Config` fields must keep the zero value valid and the map copy rule.
- **`knowledge.Store`.** Gains `Fade(now Tick) FadeResult` and `WithFader(Fader) Option`, type `Fader func(f Fact, now, elapsed Tick) (confidence float64, forget bool)`, and `FadeResult{Changed, Forgotten []string}`. `Prune`, `Find`, `Get`, `All`, `Add` signatures are unchanged. `Add`'s merge still replaces the whole fact (so an incoming fact resets `FadedAt` to its own value, normally zero).
- **`knowledge.Fact`.** Gains one field, `FadedAt Tick` (zero = never faded). It is **not** part of fact identity and `Add` does not validate it. Constructors of facts elsewhere (`spread` copies, `infer` derived facts, `declare` knowledge ops) leave it zero and need no change. The fact identity/discriminator work in Stage 2 does not interact; it must not reuse or reinterpret `FadedAt`.
- **Fact identity / discriminator.** Untouched. `Fade` never modifies `ID`, `Type`, `Subject` or `Discriminator`, and the `Fader` receives a copy whose identity changes would be ignored.

## Testing / Key Scenarios

Neutral domain: a harbour market where traders pass on news. Levels per `testing.md`: unit (table-driven, seeded/fake tick), example, integration.

| ID | Unit | Scenario | Level |
| --- | --- | --- | --- |
| S1 | U1 | Given a transform that returns `d` with `Confidence = d.Confidence + 0.1` only when `e.Teller.ID() == "gossip-bram"`, when Bram and another trader each tell the same fact, then only Bram's delivery is boosted (R1) | unit |
| S2 | U1 | Given a transform that halves confidence when `e.Listener.ID() == "sceptic-ola"`, when a fact is told to Ola and to a neutral listener, then Ola's copy is half of the neutral one (R1) | unit |
| S3 | U1 | Given a transform that halves confidence when `e.Now - e.Fact.Observed > 50`, when the same fact is told at ticks 40 and 90 from `Observed = 0`, then only the tick-90 delivery is halved (R1, R2, D5) | unit + example |
| S4 | U1 | Given a transform that tries to change `Type`, `Source`, `Prov` and return NaN, then identity fields are restored and the NaN delivery is dropped; a confidence of 1.7 is clamped to 1 (R4) | unit |
| S5 | U1 | Given `Transform == nil`, then deliveries are byte-for-byte what the existing spread tests expect; and a delivery whose post-transform confidence is below `MinConfidence` is skipped (R3, R5) | unit |
| S6 | U2 | Given a fader returning half the confidence, when `Fade(100)` runs on facts `b`, `a`, `c`, then the fader is called in order `a, b, c` and `Changed == [a b c]` (R8, R16) | unit |
| S7 | U2 | Given a fact observed at tick 0, when `Fade(100)` then `Fade(200)` run, then the fader sees `elapsed` 100 both times and `FadedAt == 200`; a fact added at tick 150 sees `elapsed` 50 at `Fade(200)` (R9, R10, R11) | unit |
| S8 | U2 | Given an expired fact, when `Fade` runs, then it is neither changed nor forgotten and remains until `Prune` (R15) | unit |
| S9 | U2 | Given a fader returning `forget == true` for one fact, and another returning a confidence below `ForgetBelow`, then both are removed and listed in `Forgotten`; `Get` returns false (R12, R16) | unit |
| S10 | U2 | Given a fader returning NaN or 1.4, then NaN leaves the fact unchanged and unsettled and 1.4 is stored as 1 (R13) | unit |
| S11 | U2 | Given a default store (no half-lives, no `WithFader`), when `Fade(1000)` runs, then nothing changes and the result is empty; `WithFader(nil)` is ignored (R17, R18) | unit |
| S12 | U3 | Given `HalfLives{"overheard": 100}`, a fact `overheard` at 0.8 observed at 0 and `ForgetBelow 0.15`, when `Fade` runs at 100, 200, 300, then confidence is 0.4, 0.2, then the fact is forgotten (0.1 < 0.15); a `witnessed` fact in the same store is unchanged (R20, R21, R12) | unit + example |
| S13 | U3 | Table: each invalid config in R24 yields `ErrInvalidConfig` (all problems joined); `DefaultConfig()` values match R22 and two calls do not share the `HalfLives` map; overriding `HalfLives` changes `Fade` behaviour (R22, R24, R25, testing rule 4) | unit |
| S14 | U4 | Given a data document with `knowledge.fade` loaded via `declare.Load` (including unknown key `knowledge.fade.half_live` which must fail), when the store is built and faded, then behaviour matches S12; plus the README snippet compiles as an `Example_` (R23, R27, R28) | integration + example |

Also required: `go test -race ./...` and a determinism test that two identical `Fade` runs on cloned stores give identical `FadeResult` and `All()` (testing rule 6).
