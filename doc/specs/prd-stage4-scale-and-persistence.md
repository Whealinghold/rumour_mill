# Stage 4: scale and persistence PRD

Status: draft · Owner: Andy Beak · Last updated: 2026-10-18

Roadmap source: `doc/roadmap.md`, Stage 4 ("only when measured"). Amends (does not replace) [knowledge](knowledge.md) §5 and, if an optimisation ships, its `Store` doc comment. Conventions followed: `doc/rules/specs.md` (spec sections, spec before code), `doc/rules/architecture.md` (rule 8 storage, rule 9 smallest surface, rule 10 concurrency, rule 11 determinism), `doc/rules/testing.md` (table-driven, seeded RNG, fake tick, `-race`, `go vet`, `justfile` entry points). There is no test-pyramid doc beyond `testing.md`; levels used below are **unit** (table-driven, one package), **example** (`Example_` test), **integration** (two or more packages) and **benchmark** (`testing.B`, measurement not pass/fail). There is no UI surface.

Sibling PRDs read: `prd-stage1-richer-rumour-behaviour.md` (`Fade`, `WithFader`, `Fact.FadedAt`) and `prd-stage5-deeper-semantics.md` (`WithConflictPolicy`, `Fact.Roles`, `Query.Where`). Their interfaces are reconciled in "Cross-stage interfaces".

## Problem & Goal

**Who.** The *consumer* is a game or simulation developer. The *end user* is the player or observer of a simulation with many NPCs, who needs the world to keep ticking smoothly (no stalls when rumours spread) and, in a game that saves, to get the same world back after loading.

**Today.**
1. Nobody knows where the library stops being fast. `Store.Find`, `All` and the `MaxFacts` eviction loop sort every fact ID on every call (`knowledge/store.go`: `slices.Sorted(maps.Keys(s.facts))`), and eviction rescans the whole store once per evicted fact. `knowledge.md` §5 says this is "fine for NPC-sized stores" but gives no number. `Spread` and `infer.Run` call `Find` once per candidate/requirement per pass, so the cost multiplies. A consumer sizing a simulation (hundreds of NPCs, thousands of facts each) cannot tell if the library will be a bottleneck, and the maintainers cannot tell whether an optimisation is worth its complexity.
2. Persistence is possible but unstated. `Store.All()` is documented as "for persistence/debug" and `Add` accepts a fact back, but nothing promises that `All()` fed into `Add` on a fresh store reproduces the original. A consumer writing a save game has to guess whether expired facts, facts below the query floor, `Private` flags, `Prov.Premises`, `Expiry` and (after Stage 1) `FadedAt` survive, and whether reloading can silently evict or merge facts. A wrong guess corrupts a save, which the end user experiences as NPCs who forgot, or suddenly believe, things.
3. Architecture rule 8 forbids a persistence interface without a real second backend or consumer-side test seam. Neither exists, so the right Stage 4 outcome for persistence is a written contract and a test, not an interface. The PRD must say that, and say what would change the answer.

**Goal.** (a) Replace guesses about scale with repeatable benchmark numbers recorded in the repo. (b) Ship an internal optimisation only where a benchmark shows it is needed, with zero change to exported API and zero change to any result. (c) Make "save with `All()`, load with `Add`" a tested, documented guarantee.

**Success measures.**
- A `just bench` run produces numbers for every operation in R1-R3 at every reference size, and they are committed to `doc/design/benchmarks.md` with the machine and Go version.
- Each optimisation shipped has a recorded before/after benchmark that clears its go/no-go gate (R10); a gate that is not cleared means the optimisation is not built, and that is a successful Stage 4 outcome.
- The exported API diff for this stage is empty (R14).
- The round-trip contract test (R6-R8) passes for every `Fact` field, including those added by Stages 1 and 5.

**Not the goal.** Making the store a database; supporting concurrent access; shipping a storage backend, file format or codec; parallelising `Spread` or `Run`.

## Scope

### In scope
- U1 Benchmarks for `knowledge`, `spread` and `infer`, a `just bench` target and a recorded results document.
- U2 A tested, documented persistence round-trip contract on the existing `All()`/`Add` pair, including the documented limits of reloading.
- U3 Internal, unexported indexing of `knowledge.Store`, built only for operations whose benchmark fails its gate.
- U4 A documented decision record for the persistence seam: no interface now, the trigger conditions, and how a consumer builds its own seam today with a consumer-defined interface.

### Out of scope
- Any exported type, function, method, option or config field (R14). If a benchmark shows an optimisation cannot be done without one, that finding is recorded and the optimisation is deferred to a new roadmap item.
- A persistence interface, `Restore`/`Load` method, serialisation codec, struct tags on `Fact`/`Provenance`, files, database or network code (lead decision; architecture rule 8; "Out of scope" in architecture.md).
- Concurrency changes: stores stay "not safe for concurrent use" (rule 10). No parallel `Spread` or `Run`.
- Dirty-tracking or incremental re-evaluation in `infer.Run` (deferred; a new roadmap item if `Run` benchmarks justify it).
- Making `Prune` automatic, or changing any cap, eviction or merge rule.
- A fixed target store size. The numbers the library must reach are an open assumption (A1), not decided here.

### Priority
| Unit | Priority | Why (end-user impact) |
| --- | --- | --- |
| U1 Benchmarks | Must | Every other decision in this stage depends on it; costs no API |
| U2 Round-trip contract | Must | A corrupted save is visible to the end user; the cost is one test and some docs |
| U4 Seam decision record | Should | Prevents a premature interface; docs only |
| U3 Indexing | Should, gated | Only valuable if U1 shows a real problem; first to cut, and cutting it is legitimate |

## Requirements

### U1 Benchmarks
- **R1.** `knowledge` has benchmarks, each over a seeded random store (`*rand.Rand` seeded by a constant in the test, per testing rule 6), for: `Add` (new fact), `Add` (merge into an existing ID), `Add` that triggers `MaxFacts` eviction, `Find` by `Type`, `Find` by `Subject`, `Find` by `Type`+`Subject`, `Find` with no filter, `Get`, `All`, `Prune` (a given fraction of facts expired) and, once Stage 1 lands, `Fade`.
- **R2.** `spread` has benchmarks for one `Spread` call with a teller holding N facts and L listeners, with and without a filter and with and without a `Transform`.
- **R3.** `infer` has benchmarks for one `Run` over a store of N facts with a rule set of one, five and twenty rules, including a case where nothing fires and a case that runs to `MaxPasses`.
- **R4.** Every benchmark runs at several reference sizes as `b.Run` sub-benchmarks: facts per store, holders and listeners are table parameters, not constants buried in the body. The size lists are open assumption A1. The size list includes at least one size where the current O(n log n) per-call sort is visible, so the numbers can show a curve rather than a point.
- **R5.** Every benchmark calls `b.ReportAllocs()`. Benchmark setup is excluded from timing with `b.ResetTimer`.
- **R5a.** `justfile` gains `bench`, running `go test -run '^$' -bench . -benchmem ./...`. Benchmarks are not run by `test`, `test-race` or `lint`, so the existing entry points stay fast.
- **R5b.** `doc/design/benchmarks.md` records, per benchmark and size: ns/op, B/op, allocs/op, the Go version, GOOS/GOARCH, CPU model and the commit. It is regenerated by hand when an optimisation ships. It states the go/no-go decision for each candidate optimisation (R10) with the numbers that justified it.

### U2 Persistence round-trip contract
- **R6.** For any store `A`, a fresh store `B` built with the same `Config` and no conflict policy (Stage 5) into which every fact of `A.All()` is `Add`ed in the order returned satisfies: `B.All()` is deeply equal to `A.All()`, including `ID`, `Type`, `Subject`, `Discriminator`, `Payload`, `Confidence`, `Source`, `Prov` (all four fields, with `Premises` order preserved), `Observed`, `FadedAt` (Stage 1), `Expiry` (nil stays nil), `Private` and, once Stage 5 lands, `Roles`.
- **R7.** The contract holds for the facts `Find` hides: expired facts, facts below `QueryFloor`, and facts whose `Source` is absent from `SourceRanks`.
- **R8.** The contract holds for a store at its `MaxFacts` cap: reloading `A.All()` into a store with the same `Config` evicts nothing and returns `Outcome == Inserted` for every fact, because `A` never held more than the cap.
- **R9.** Reloading does **not** guarantee the same result if the target store is non-empty, has a smaller `MaxFacts`, or uses a different `MergePolicy`: `Add` applies the merge policy and the cap, as it always does. The `Store` doc comment and `knowledge.md` state the guarantee as "load into an empty store with the same configuration" and name these three limits. No `Restore` method is added.
- **R9a.** The test is table-driven over the cases above plus a randomised case that builds a store through a seeded sequence of `Add`s (with merges and evictions), and, once Stage 1 exists, `Fade` and `Prune`, then asserts the round trip.
- **R9b.** The docs state that encoding is the caller's: `Payload` is `any` and cannot be serialised generically, `Fact` carries no struct tags, and the library ships no codec. The README or `knowledge.md` includes one short example in a neutral domain of a consumer serialising `[]knowledge.Fact` with `encoding/json` and a typed payload, as a consumer-owned pattern (an `Example_` test, so it compiles and runs).

### U3 Internal indexing (gated)
- **R10.** Each candidate optimisation below is built only if its benchmark fails its gate. The gate for candidate *c* is: the operation's cost at a reference size named in A1 exceeds the threshold named in A1. The decision (build or not) and the evidence are recorded in `doc/design/benchmarks.md` before the optimisation code is written, per spec-before-code (specs rule 1). Candidates:
  - **C1** Maintain the sorted ID order incrementally instead of sorting on every `Find`, `All` and eviction scan.
  - **C2** Secondary maps from `Type` and from `Subject` to fact IDs so `Find` with those fields examines only matching facts.
  - **C3** Replace the per-eviction full rescan in `enforceCap` with an ordered structure keyed by weakness (rank, confidence, `Observed`, ID).
  - **C4** An expiry-ordered structure so `Prune` visits only expired facts.
- **R11.** Every index is unexported, owned by `Store`, and never visible in a result. All store mutations (insert, replace, delete, confidence change, expiry change) go through exactly two unexported helper methods, so no code path can change a fact without updating every index. `Add`, `Prune`, eviction, Stage 1 `Fade` and Stage 5 conflict resolution all use them.
- **R12.** Indexed keys are re-keyed when the fact they describe changes: weakness for C3 changes with `Confidence`, `Source`, `Observed`; expiry order for C4 changes with `Expiry`. A merge `Replace`, a `Fade` confidence change and a Stage 5 `Resolution.Confidence` change each re-key (tested, R13).
- **R13.** Results are unchanged: for any sequence of operations, `Add` results (`Outcome`, `Evicted` and its order), `Find`, `Get`, `All`, `Prune` counts and `Fade` results equal those of the current implementation. Verified by an equivalence test that runs seeded random operation sequences (`Add` with merges and rejects, `Add` that evicts, `Prune`, `Find` with every `Query` field combination, `Fade`) against both the indexed store and a naive reference store kept in a `_test.go` file (the current implementation, preserved as the oracle), asserting equal results after every step.
- **R14.** The exported API of `knowledge`, `spread`, `infer`, `rules`, `declare` and `logic` is byte-for-byte unchanged by this stage. Verified by a check against the output of `go doc -all` (or `go list -json` export data) recorded before the stage starts, run in CI-equivalent form as a `just` target or a test.
- **R15.** No new dependency (`go.mod` unchanged). Standard library only (`sort`, `slices`, `container/heap`, `maps`).
- **R16.** Memory overhead of the indexes is measured (B/op and a store-size heap measure in the benchmark) and recorded in `doc/design/benchmarks.md`. A candidate whose index more than doubles per-fact memory while improving its target operation by less than a factor named in A1 is not shipped.
- **R17.** Deterministic: the index never iterates a Go map to produce ordered output. Orders come from the sorted structures, so results do not depend on map iteration order (architecture rule 11).
- **R18.** `go test -race ./...` and `go vet ./...` stay clean. Concurrency docs are unchanged: `Store` is still not safe for concurrent use, and the new internal state does not change that.
- **R19.** If any candidate ships, the `Store` doc comment and `knowledge.md` §5 replace "fine for NPC-sized stores" with the measured figures and the remaining limits. If none ships, §5 gains the measured figures that justify keeping the sort.

### U4 Persistence seam decision record
- **R20.** `knowledge.md` (or `doc/design/`) gains a short "Persistence" section stating: the library has no storage interface; why (architecture rule 8); what triggers adding one (a real second backend, or a consumer-side test seam that cannot be met another way); that Go's implicit interfaces already let a consumer define `type factSource interface { All() []knowledge.Fact }` or a similar one-or-two-method interface in its own package and wrap `*knowledge.Store` today; and the candidate shape, labelled non-normative, a future interface would be reviewed against (small, consumer-shaped, `All`/`Add`, with `context.Context` only if a backend can do I/O, per architecture "Idiomatic Go" rule 6).
- **R21.** The section states the data a persisted store must capture, derived from R6: the facts from `All()`, plus the `knowledge.Config` and merge policy the consumer used (they are code and config, not stored facts).
- **R22.** No code is added for U4 beyond the example in R9b. Roadmap item "Persistence seam" is marked "decided: no interface; revisit on trigger" when this PRD is accepted.

## Non-Functional Requirements

### Performance, Scale
- This stage *measures* performance; it sets no absolute target (A1). The relative requirement is: no shipped optimisation regresses any other benchmark by more than the noise margin recorded in `benchmarks.md` (A2).
- Complexity before and after is stated per operation in `knowledge.md` §5 (R19).
- The unused path stays cheap: a store with a handful of facts must not be measurably slower after indexing than before (benchmark at the smallest reference size is part of the R1-R3 tables and compared before and after).

### Security
- No new input surface; no I/O. Round-trip tests use values, not files. N/A beyond that.

### Reliability
- The equivalence test (R13) is the correctness backstop: it is the reason indexing can be added without risking save/gameplay behaviour.
- The round-trip contract (R6-R9) is the reliability requirement for persistence.

### Privacy / Compliance
- N/A: the library holds caller-supplied data in memory and writes nothing. `Private` facts are included in `All()` because they are the holder's own beliefs; this is existing behaviour, restated in the persistence section so a consumer knows saved data includes private beliefs.

### Observability
- The library has no logging or metrics. The observability surface for this stage is `doc/design/benchmarks.md` and `just bench`. A regression is detected by re-running `just bench` and comparing against the recorded numbers (A2).

### Rollout & Release Safety
- Library-internal change with an empty exported diff (R14), so no consumer migration. Ships as one change per unit so each can be reverted alone; U3 candidates ship one at a time, each with its own benchmark entry.
- `CHANGELOG.md` notes: new `just bench` target; persistence contract documented and tested; and, per shipped candidate, "internal indexing, no API change".

## UX & Design

N/A: no user-facing surface. The equivalent concern, developer experience, is handled by usage-first design (architecture "Idiomatic Go" rule 1): the persistence `Example_` (R9b) is written first and the documentation wording adjusted until it reads well. Consistency with existing conventions: benchmarks follow the table-driven style of existing `_test.go` files, `just` target names match the existing `test`/`test-race`/`lint` style, and docs live under `doc/` (never `docs/`).

## Stakeholders & Sign-off / Acceptance Criteria

- **Owner:** Andy Beak. **Approver:** the user (specs rule 1: spec approved before code). **Reconciler:** the lead agent, for cross-stage interfaces.
- **Done when:** every acceptance criterion in the Phases table below is met, `go test ./...`, `go test -race ./...` and `go vet ./...` pass, `docs_test.go` and `TestNoDomainVocabulary` pass, `doc/README.md` lists this PRD and the benchmarks document (lead action), and `knowledge.md` is updated in the same change as any code it describes (specs rule 5).

## Phases / Units

| Unit | Depends on | Risk | Decision or assumption | Acceptance criterion |
| --- | --- | --- | --- | --- |
| U1 Benchmarks (R1-R5b) | none (Stage 1 `Fade` benchmark added when Stage 1 lands) | Low: tests only. Benchmark noise on shared machines (A2). | Decided: benchmarks first, in `_test.go`, `just bench`, no new API, no dependency. Assumed: reference sizes (A1). | `just bench` completes; `benchmarks.md` has a row for every R1-R3 benchmark at every size; `test`, `test-race` unaffected. |
| U2 Round-trip contract (R6-R9b) | U1 not required. Needs Stage 1 `FadedAt` and Stage 5 `Roles` fields if those have landed; otherwise tests cover the fields that exist and the test is extended when they land. | Low-medium: surfaces any existing field `Add`/`clone` mishandles, which would be a bug fix, not a scope change. Stage 5 conflict policy interaction (see Cross-stage interfaces). | Decided: load-into-empty guarantee, no `Restore`, no codec, no tags. | Round-trip test green for the cases in R6-R9a; docs state the three limits (R9); `Example_` for consumer-owned encoding runs. |
| U3 Indexing (R10-R19) | U1 (gate evidence); U2 (round-trip test is a regression guard); Stage 1 `Fade` and Stage 5 conflict policy interfaces, if landed, because they mutate facts | Medium: the index must stay consistent with every mutation path (R11-R12); a stale index silently returns wrong results. Gate may show no optimisation is warranted. | Decided: unexported only, gated by benchmark, one candidate at a time. Assumption: thresholds (A1). | Per shipped candidate: gate evidence recorded before code; R13 equivalence test green over at least the randomised sequences specified; R14 API-diff check empty; after-benchmark shows the improvement in `benchmarks.md`; no other benchmark regresses beyond noise. If no gate fires: "no optimisation needed" recorded and U3 is closed with no code. |
| U4 Seam decision record (R20-R22) | U2 (R21 refers to the contract) | Low | Decided: no interface now. | Section present in `knowledge.md`/`doc/design/`; names trigger, consumer-defined-interface pattern and non-normative candidate shape; roadmap line updated by the lead. |

Order: U1 → (U2 in parallel) → U3 for gated candidates → U4 (can be written any time after U2). U1 is the only hard prerequisite for U3.

## Testing / Key Scenarios

| # | Unit | Scenario | Level |
| --- | --- | --- | --- |
| S1 | U1 | Given a seeded store of N facts for each reference N, when `just bench` runs, then every R1 benchmark reports ns/op, B/op, allocs/op at every size and the run is repeatable on one machine within the noise margin | benchmark |
| S2 | U1 | Given `Spread` with L listeners and N teller facts, when benchmarked with and without a filter and `Transform`, then both variants are reported and the filter/transform cost is separable | benchmark |
| S3 | U1 | Given 1, 5 and 20 rules over N facts, when `Run` is benchmarked for "nothing fires" and "runs to `MaxPasses`", then both cases are reported per rule count | benchmark |
| S4 | U2 | Given a store with an expired fact, a fact below `QueryFloor`, an unranked-source fact, a private fact, a fact with `Premises` and `Expiry`, when `All()` is `Add`ed to a fresh same-config store, then the new `All()` deep-equals the old | unit |
| S5 | U2 | Given a store at `MaxFacts`, when its `All()` is reloaded into a same-config store, then every `Add` returns `Inserted`, `Evicted` is empty, and `All()` equals the original (R8) | unit |
| S6 | U2 | Given a seeded random sequence of `Add` (merges, rejects, evictions), `Prune` and (once available) `Fade`, when the final `All()` is reloaded, then it equals the original | unit |
| S7 | U2 | Given a target store with a smaller `MaxFacts`, when the same facts are reloaded, then eviction occurs and the test asserts the documented limit rather than equality (R9) | unit |
| S8 | U2 | Given a consumer-defined payload type, when facts are serialised with `encoding/json` and decoded back with a typed payload and reloaded, then the store matches (the example is the docs, R9b) | example |
| S9 | U3 | Given the indexed store and the naive oracle and a seeded random operation sequence, when each op runs on both, then results (`Outcome`, `Evicted` order, `Find`, `Get`, `All`, `Prune` count) are equal after every step, for several seeds and for stores capped and uncapped | unit |
| S10 | U3 | Given a fact whose confidence is changed by a merge `Replace`, by `Fade` or by a Stage 5 resolution, when the weakest is evicted, then the order matches the oracle (re-keying, R12) | unit |
| S11 | U3 | Given an expiry change by `Replace` and a `Prune` call, then only expired facts are removed and the count matches the oracle (C4, R12) | unit |
| S12 | U3 | Given the exported API before and after the stage, then the API diff is empty (R14) | unit/script |
| S13 | U3 | Given a benchmark that does not fail its gate, then the stage records "no optimisation" and no index code exists (R10) | review |
| S14 | U3 | Given a hot-path fuzz of `Add` with random IDs, types, subjects and confidences, then the indexed store never panics and agrees with the oracle (also protects against U+001F and empty-field edge cases) | unit (fuzz) |

Also required: `go test -race ./...`, `go vet ./...`, and the leak and docs guard tests, per testing rule 9.

## Cross-stage interfaces

Assumptions this stage makes about types and hooks other stages touch, for the lead to reconcile.

- **`knowledge.Store` (Stage 1, Stage 5).** Assumed: Stage 4 adds no exported method or option. Stage 1 adds `Fade(now Tick) FadeResult` and `WithFader(Fader)`; Stage 5 adds `WithConflictPolicy(ConflictPolicy)`. Assumed: **every** state change in the store, including `Fade` confidence updates and `FadedAt`, `Prune`, `MaxFacts` eviction, merge `Replace`, and Stage 5 `Resolution.Remove` and `Resolution.Confidence`, goes through the two unexported mutation helpers (R11). Stage 1 and Stage 5 implementations must not write to the facts map directly. Order of operations in `Add` is as Stage 5 states (validate, clamp, identity, merge decision, conflict policy, write, `MaxFacts` eviction); Stage 4 preserves it.
- **`knowledge.Fact` (Stage 1 `FadedAt`, Stage 5 `Roles`).** Assumed: `All()` → `Add` is lossless for every field. `FadedAt` is not validated, not part of identity and not rewritten by `Add`: it round-trips as stored. `Roles` is deep-copied by `clone`; the round-trip test (R6) is extended to compare it. **Any other stage adding a `Fact` field must extend the R6 round-trip test and keep `clone` deep.**
- **`knowledge.Config` (Stage 1 `Fade`).** Assumed unchanged by Stage 4. A store's persisted state is `All()` plus the config and merge policy the consumer holds; `Config.Fade` is configuration, not store state.
- **Stage 5 conflict policy vs reload (assumption this stage breaks or must qualify).** `Add` runs the conflict policy on every insert. Reloading `All()` through `Add` into a store that has a conflict policy installed may reject, remove, or re-weight facts that were consistent in the original (the policy sees them arriving one at a time). The load-into-empty guarantee (R9) is therefore stated for a store built **without** a conflict policy. Options are construction-time, so a consumer cannot load first and attach the policy afterwards. **Lead decision needed:** either (a) accept that persistence into a policy-equipped store is not guaranteed and document it (recommended; no new API), or (b) Stage 5 gives the conflict policy a way to see that it is loading. Stage 4 assumes (a).
- **Stage 5 `Query.Where` and `Query.Roles`.** Assumed: an index narrows candidates by `Type` and `Subject` only; `Roles` and `Where` filters run on the narrowed set exactly as today, so `Find` results are unchanged (Stage 5 §9 item 11 already assumes this). `Where` is a caller function and cannot be indexed.
- **Stage 5 conflict lookup uses `Find` semantics.** The `find` closure passed to a `ConflictPolicy` is served by the same `Find` path, so it benefits from, and must stay consistent with, the index. A conflict policy that calls `find` re-entrantly *during* `Add` must see the store as it stands before the incoming fact is written; the index must not be mid-update at that point (update the index in the same helper as the write, after the policy returns).
- **`Spreader.Transform`, `Spreader` filters, `Encounter` (Stage 1, Stage 2).** Untouched by Stage 4. Benchmarks (R2) call them as shipped; if Stage 1 widens `Transform`'s signature, the R2 benchmark is updated, not the API.
- **`declare.Catalog` (Stage 2).** Untouched. No benchmark covers `declare` loading (load time is not on the simulation hot path); a consumer can file a roadmap item if it becomes one.
- **Fact identity / discriminator (Stage 2, Stage 5).** Assumed: ID is an opaque string key and fact identity continues to be `Type`, `Subject`, `Discriminator` (or an explicit `ID`). The `Type` and `Subject` secondary indexes (C2) key on the stored `Type` and `Subject` fields, not on the ID text, so a change to how IDs are derived does not affect them. `ErrIdentityMismatch` guarantees an ID never changes Type/Subject, so the secondary indexes need no re-keying on `Replace`.
- **Stage 5 `AddResult.Conflicted`.** Assumed an additive field that the equivalence oracle (R13) compares like the existing result fields.

## Assumptions pending validation

- **A1.** The reference sizes (facts per store, holders, listeners) and the go/no-go thresholds (the cost at which an operation fails its gate, and the minimum improvement factor in R16) are **not** decided. The lead has said not to invent a size. This PRD defines the mechanism; the numbers are filled in `doc/design/benchmarks.md` by the first accepted benchmark run and approved by the user before any U3 code is written.
- **A2.** Benchmark noise on the maintainer's machine is small enough to compare before/after. The noise margin is measured by repeating each benchmark (for example with `-count`) and recorded in `benchmarks.md`; if noise is larger than the effect, U3 is not shipped on that evidence.
- **A3.** No consumer is currently blocked by performance. If a real consumer reports a specific slow operation, that operation's gate takes priority and the consumer's size becomes the reference size.
- **A4.** Persistence into a store with a Stage 5 conflict policy is not guaranteed (see Cross-stage interfaces).
- **A5.** The `go doc -all` baseline for R14 can be committed under a test fixture or generated at the stage start; planning chooses which.

## Decisions (from lead review)

1. The benchmark results document lives at `doc/design/benchmarks.md`; the lead links it from `doc/README.md`.
2. "API unchanged" (R14) is checked by comparing `go doc -all` output for each package before and after; a golden test of `go/types` exports is not required.
3. The naive store is kept in a `_test.go` file as the test-only oracle for R13.
