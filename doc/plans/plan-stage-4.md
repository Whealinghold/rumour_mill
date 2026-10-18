# Plan: Stage 4, scale and persistence

**Status: Proposed.** Sequences [prd-stage4-scale-and-persistence.md](../specs/prd-stage4-scale-and-persistence.md) (R1-R22, U1-U4) into buildable phases. Built against `doc/rules/architecture.md` (rules 8, 9, 10, 11; "Idiomatic Go" rules 1, 2, 9), `doc/rules/testing.md` (rules 1-9) and `doc/rules/specs.md` (rules 1, 2, 5). There is no plan-format doc in the repo and no earlier plan; this file uses the generic plan template. A plan decides nothing: every product or architecture call below is either from the PRD or is listed under "Decisions" with a rationale, so it is easy to challenge.

## Scope

**In scope** (all from the PRD, none added):
- U1 benchmarks (`knowledge`, `spread`, `infer`), `just bench`, `doc/design/benchmarks.md`.
- U2 the tested, documented "save with `All()`, load with `Add` into an empty same-config store" contract, plus the consumer-owned `encoding/json` `Example_`.
- U4 the "Persistence" decision-record section (no interface; triggers; consumer-defined-interface pattern).
- U3 internal indexing of `knowledge.Store` (candidates C1-C4), each built only if its benchmark fails its gate, with an empty exported API diff.

**Out of scope** (PRD "Out of scope", restated): any exported identifier; a persistence interface, `Restore`/`Load`, codec, struct tags, files, DB, network; concurrency changes or parallel `Spread`/`Run`; dirty tracking in `infer.Run`; automatic `Prune`; any cap, eviction or merge rule change; a fixed target store size; benchmarks for `declare` loading. Deferred items become new roadmap items, not work here.

**Gating (explicit, not scheduled work):**
- **Stage gate G0.** The roadmap says Stage 4 is "only when measured" and later stages start only on real need. Phases 1-3 are cheap, API-free and safe to run once Stage 4 is activated (a maintainer decision or a consumer report, PRD A3). Nothing in Phases 4+ is scheduled.
- **Gate G1 (Phase 4)** is a human decision: the user approves the A1 thresholds in `benchmarks.md`. No U3 code before it.
- **Gate G2 (Phases 5-9)** is per candidate: built only when its benchmark fails the approved threshold. "No candidate fails" closes U3 with no code and is a successful outcome (PRD S13).
- **Stage 3's second-consumer task** is not a dependency of this plan. If a real consumer later reports a slow operation, its size becomes the reference size for that operation's gate (PRD A3) and Phase 4 is re-run.

## Assumptions this plan makes (flagged, not decided)

- **P-A1.** Benchmark grid (measurement points, not targets; one table literal per package, trivially changed): facts per store `{10, 100, 1000, 10000}`; spread teller facts `{10, 100, 1000}` x listeners `{1, 10, 100}`; infer store facts `{10, 100, 1000}` x rule counts `{1, 5, 20}` (rule counts are fixed by R3). Chosen so the O(n log n) sort curve is visible (R4). The *thresholds* are not invented here (PRD A1); Phase 4 asks the user for them.
- **P-A2.** Noise margin (PRD A2) is measured as (max-min)/median of ns/op over `-count=10` runs of the same benchmark, recorded per benchmark; before/after comparison is by median, by hand, with no `benchstat` dependency (R15).
- **P-A3.** If Stage 1 or Stage 5 have not landed when a phase runs, that phase covers the fields and methods that exist and carries a dated "extend when Stage N lands" checklist item (the PRD allows this for U2). Phases are written so the extension is a table-row addition, not a redesign.
- **P-A4.** API baseline (PRD A5): generated from the Stage 4 start commit at check time, not committed as a fixture (see Decision D3).
- **P-A5.** Candidate build order is C1, C2, C3, C4, but only the gated subset ships, in whatever order the evidence supports; each is its own change.

## Reuse and boundary findings (Step 0)

- `knowledge/store.go`: the sort sites to measure are `Find`, `All`, `enforceCap` (full rescan per evicted fact) and `Prune` (map range). Mutation sites to funnel: `Add` (`s.facts[f.ID] = f` twice), `enforceCap` (`delete`), `Prune` (`delete`). `weaker` and `rank` are reused unchanged as the heap comparator source for C3.
- `knowledge/store_test.go`, `example_test.go`, `spread/helpers_test.go`, `spread/spread_test.go`, `infer/infer_test.go`: mirror their table-driven style and existing fakes/fixtures for benchmark setup; do not add new fixtures where they exist.
- `justfile` has `test`, `test-race`, `lint`; add `bench` in the same style.
- `architecture_test.go` already parses source with `go/parser`; the "no direct writes to `s.facts` outside the two helpers" guard (R11) reuses that approach in a `knowledge` package test.
- `docs_test.go` and `TestNoDomainVocabulary` (`leak_test.go`) must stay green; benchmark, example and doc text use a neutral domain (village notice-board, office gossip).
- Import boundaries (`architecture_test.go`): `knowledge` imports nothing; benchmarks in `spread`/`infer` may import `knowledge` only. Index code lives in package `knowledge` (unexported); no new package and no `internal/` package is needed (R9 smallest surface).

## Phase 0 - Activation and API baseline

**Why first:** R14 needs a "before" reference taken before any code, and G0 must be explicit.
- Record the activation reason (maintainer decision or consumer report) in the Phase 0 commit message; record the base commit hash.
- Add `just api-check base="<ref>"` (no deps): in a temporary `git worktree` of `<ref>`, run `go doc -all` for `.`, `./knowledge`, `./spread`, `./infer`, `./rules`, `./declare`, `./logic`, run the same on the working tree, `diff` the two, clean up the worktree, exit non-zero on difference. Lives in `justfile` (Decision D3; PRD Decision 2 says compare `go doc -all`).
- Demonstrate the target works: run it against `HEAD` with a deliberately added exported symbol (not committed) and confirm it fails.

**Tests:** manual one-off proof above (a script target; no Go test, because a Go test cannot see the base ref). Unit level N/A.
**Observability & rollback:** target prints the diff on failure. Rollback = delete the target; nothing else depends on it until Phase 5.
**Acceptance:** `just api-check <start-commit>` exits 0 on a clean tree and non-zero when an exported symbol is added.

## Phase 1 - U1 Benchmarks (R1-R5b)

**Why first:** Every U3 decision and the A1 numbers depend on it; it adds no API (PRD: "U1 is the only hard prerequisite for U3").
- `knowledge/bench_test.go`: sub-benchmarks (`b.Run`) over the P-A1 sizes for `Add` new, `Add` merge, `Add` triggering `MaxFacts` eviction, `Find` by `Type`, by `Subject`, by `Type`+`Subject`, no filter, `Get`, `All`, `Prune` at a given expired fraction (fraction is a table parameter). Stores built from a constant-seeded `*rand.Rand` (testing rule 6); setup excluded via `b.ResetTimer`; `b.ReportAllocs()` everywhere (R5).
- `spread/bench_test.go`: one `Spread` call, teller N facts x L listeners, four variants (filter yes/no x `Transform` yes/no; write the `Transform` variant against whichever signature exists in the tree, `func(knowledge.Fact) knowledge.Fact` or Stage 1's `func(Encounter, knowledge.Fact) knowledge.Fact`, with a P-A3-style checklist note to migrate it if Stage 1 lands later), reusing `spread/helpers_test.go` fixtures. Filter and transform costs must be separable (S2): the no-filter/no-transform variant is the baseline row.
- `infer/bench_test.go`: `Run` over N facts with 1/5/20 rules, "nothing fires" and "runs to `MaxPasses`" (S3).
- `justfile`: `bench count="1"` running `go test -run '^$' -bench . -benchmem -count {{count}} ./...` (default identical to R5a). `test`, `test-race`, `lint` unchanged and do not run benchmarks.
- `doc/design/benchmarks.md` (new): environment header (Go version, GOOS/GOARCH, CPU model, commit), one row per benchmark and size (ns/op, B/op, allocs/op), noise margin per P-A2, candidate list C1-C4 with the decision column left "pending G1", and a regeneration recipe. Every PRD scenario S1-S3 is checked by running it.
- **If Stage 1 has landed:** add a `Fade` benchmark (R1). **If not:** a checklist line in `benchmarks.md` "add Fade when Stage 1 lands".

**Tests:** benchmarks are measurement, not pass/fail. A cheap guard: each benchmark body also runs once under plain `go test` via a tiny `TestBenchSetupsRun` per package (calls each setup builder once and asserts store size), so setup rot is caught by `just test` without running benchmarks. Level: unit (setup) + benchmark.
**Observability & rollback:** `benchmarks.md` is the observability surface (PRD). Revert = delete the `_test.go` files, the target and the doc; no production code touched.
**Acceptance:** `just bench` completes; `benchmarks.md` has a row for every R1-R3 benchmark at every size; `just test`, `just test-race`, `just lint` unaffected and green.

**Spec/doc updates in the same change:** `doc/design/benchmarks.md` (new). `CHANGELOG.md`: "new `just bench` target". (`doc/README.md` link is a lead action per PRD Decision 1.)

## Phase 2 - U2 Persistence round-trip contract (R6-R9b)

**Why here:** Independent of Phase 1 (can run in parallel); must exist before any indexing so it is the regression guard (U3 depends on it).
- Usage first (Idiomatic Go rule 1): write the consumer `Example_` first (R9b): a neutral-domain type (for example a notice-board `Posting` payload) serialised as `[]knowledge.Fact` with `encoding/json`, decoded with a typed payload, reloaded into a fresh same-config store, output asserted. Adjust the doc wording until it reads well.
- `knowledge/roundtrip_test.go`, table-driven helper `roundTrip(t, cfg, A)` that reloads `A.All()` in order into `NewStore(cfg)` (no conflict policy) and asserts `reflect.DeepEqual(B.All(), A.All())`, every `Add` is `Inserted` and `Evicted` is empty. Cases: S4 (expired fact, fact below `QueryFloor`, unranked-source fact, `Private` fact, fact with `Premises` order and `Expiry`; also nil vs empty non-nil `Premises` and nil `Expiry`), S5 (store at `MaxFacts`), S6 (seeded random sequence of `Add` with merges/rejects/evictions plus `Prune`), S7 (smaller `MaxFacts` target: assert eviction occurs, not equality, documenting the limit), plus the other two R9 limits (non-empty target, different `MergePolicy`) each as a table row asserting the documented non-guarantee.
- Fix, as a bug, any field `Add`/`clone` mishandles that the test surfaces (PRD risk note). A field fix here is allowed; an API change is not.
- Docs: `knowledge.md` and the `Store` doc comment state "load into an empty store with the same configuration", name the three limits (R9), say encoding is the caller's and there are no struct tags/codec (R9b), and restate that `All()` includes `Private` beliefs (PRD Privacy).
- **P-A3 hooks:** if Stage 1 landed, `FadedAt` is in the random case and a `Fade` step is in S6; if Stage 5 landed, `Roles` (deep-copied) is compared, `Query.Roles`/`Where` are not persistence concerns, and a table row asserts the documented limit that reloading into a store with a `ConflictPolicy` is not guaranteed (A4, decision (a), no new API). Otherwise add them as a checklist in the test file header. Any later stage adding a `Fact` field extends `roundTrip` and keeps `clone` deep (cross-stage rule).

**Tests:** unit (table-driven, one package) for S4-S7; example for S8; all hermetic, seeded RNG (testing rules 2, 6). Run with `-race` and `go vet`.
**Observability & rollback:** none beyond test output (library has no logs). Revert = delete the test file and doc edits; only a `clone`/`Add` bug fix (if any) needs its own revert, so land it as a separate commit.
**Acceptance:** S4-S8 green; `Example_` runs; docs state the three limits; `docs_test.go` and leak guard green.

**Spec/doc updates in the same change:** `doc/specs/knowledge.md` (persistence guarantee, limits, encoding example reference), `Store` doc comment in `knowledge/store.go`, `CHANGELOG.md` ("persistence contract documented and tested").

## Phase 3 - U4 Persistence seam decision record (R20-R22)

**Why here:** Needs the Phase 2 contract (R21 derives the persisted data from R6). Docs only.
- Add a "Persistence" section to `doc/specs/knowledge.md` (kept next to the contract rather than a new file, Decision D5): no storage interface and why (architecture rule 8); triggers for adding one (a real second backend, or a consumer-side seam not otherwise satisfiable); the consumer-defined `type factSource interface { All() []knowledge.Fact }` pattern with `*knowledge.Store` satisfying it today; the persisted-state list (`All()` + the `knowledge.Config` and merge policy the consumer holds; `Config.Fade` is config, not state); the non-normative candidate shape (small, consumer-shaped `All`/`Add`, `context.Context` only if a backend does I/O).
- No code (R22). Roadmap line "Persistence seam" -> "decided: no interface; revisit on trigger" is a lead action once the PRD is accepted; this plan lists it, does not edit it.

**Tests:** `docs_test.go` and the leak guard cover the doc edits; the claim that `*knowledge.Store` satisfies a consumer interface is compile-checked inside the Phase 2 `Example_` (declare the one-method interface there). Level: example.
**Observability & rollback:** docs only; revert the section.
**Acceptance:** section present, names trigger, pattern, persisted-state list and the non-normative shape.

## Phase 4 - GATED (G1): benchmark review and go/no-go

**Why:** R10 requires the build/no-build decision and its evidence to be recorded before any index code (spec before code). This is a human decision phase, not implementation.
- Run `just bench 10` on the maintainer machine; record medians and noise margins in `benchmarks.md`.
- Propose, and the user approves, the A1 values: the cost threshold per candidate operation at a named reference size, and the R16 minimum improvement factor. The plan invents none of them.
- For each of C1-C4 write "build" or "no optimisation" with the numbers. If all four are "no": add the measured figures to `knowledge.md` section 5 ("fine for NPC-sized stores" gets the measured numbers it lacked, R19), mark U3 closed, skip Phases 5-10 body work except Phase 10's docs/changelog items, and stop.
- Re-open this phase if a consumer reports a slow operation (A3).

**Tests:** none (decision record). Review level: S13.
**Observability & rollback:** `benchmarks.md` is the artifact. Nothing to roll back.
**Acceptance:** decision recorded per candidate with evidence, user-approved thresholds present, committed before any Phase 5+ code.

**Spec/doc updates in the same change:** `doc/design/benchmarks.md`; `doc/specs/knowledge.md` section 5 (measured figures, R19) when no candidate ships.

## Phase 5 - GATED (G2, entered only if at least one candidate is "build"): oracle, mutation helpers, guard

**Why first among U3 phases:** The oracle and funnel make every later index safe and are shared by all candidates (R11, R13).
- Copy the current `Store` logic into `knowledge/naive_test.go` as `naiveStore` (test-only oracle; PRD Decision 3), including `Prune`, eviction and, if landed, `Fade` and conflict-policy handling, written from the pre-index code at the Stage 4 base commit.
- Refactor `store.go` so every fact-map change goes through exactly two unexported helpers, `put(f Fact)` (insert, replace, confidence/expiry change) and `drop(id string)` (delete); `Add`, `enforceCap`, `Prune` and, if Stage 1 has landed, `Fade` (an explicit call site to migrate, with a `Fade` case in the oracle and equivalence tests) and Stage 5 `Resolution.Remove`/`Confidence` use only these (R11). No index yet, so this refactor is behaviour-identical.
- `knowledge/store_guard_test.go`: parse `store*.go` with `go/parser` (same technique as `architecture_test.go`) and fail if `s.facts[...] =` or `delete(s.facts` appears outside `put`/`drop`. This keeps Stage 1 and Stage 5 code honest (cross-stage obligation).
- `knowledge/equiv_test.go`: seeded random operation sequences (`Add` with merges, rejects and identity mismatches, `Add` that evicts, `Prune`, `Find` over every `Query` field combination including `MinConfidence` nil/set and `Sources`, `Get`, `All`, plus `Fade` and conflict policy if landed) run against `Store` and `naiveStore` for several seeds, capped and uncapped, comparing `AddResult` (`Outcome`, `Evicted` order, `Conflicted` if landed), `Find`, `Get`, `All` and `Prune` counts after every step (S9). Add `FuzzStoreAgainstOracle` for random IDs/types/subjects/confidences including empty fields and U+001F inputs (S14). With no index the equivalence is trivially true; its value is that it exists and is green before C1-C4.
- Run `just api-check <start-commit>`: empty diff.

**Tests:** unit (equivalence, guard) + fuzz; `-race` clean. Oracle is a test file so production code carries no duplicate.
**Observability & rollback:** test failures print seed and step index. Revert = one commit; behaviour-identical so a revert is safe at any time.
**Acceptance:** guard, equivalence and fuzz seed corpus green with no index; `go test -race`, `go vet`, `api-check` clean; the Phase 1 benchmarks show no regression beyond noise from the helper indirection.

## Phase 6 - GATED (G2, only if C1 "build"): C1 incremental sorted ID order

**Why this order:** It removes the `slices.Sorted(maps.Keys(...))` cost from `Find`, `All` and the eviction scan, and its sorted-slice helper is reused by C2.
- Maintain a sorted `[]string` of IDs in `Store`, updated only inside `put`/`drop` by binary search (`sort.SearchStrings`/`slices.BinarySearch`) with `slices.Insert`/`slices.Delete`. `Find`, `All` and `enforceCap` iterate it instead of sorting. No map iteration produces ordered output (R17).
- Equivalence test (existing) must stay green; add S10-style re-key checks N/A here (order depends only on ID).
- Record the after-benchmark in `benchmarks.md` including the smallest-size comparison (the unused path stays cheap) and heap/B-per-fact overhead (R16).

**Tests:** existing equivalence + fuzz; benchmark before/after; `-race`, `vet`, `api-check`.
**Observability & rollback:** `benchmarks.md` before/after row; revert the single commit (guard keeps `put`/`drop` as the only touch points, so the revert is local).
**Acceptance:** gate recorded earlier; equivalence green; target operation clears the approved improvement factor; no other benchmark regresses beyond noise; memory within R16.

## Phase 7 - GATED (G2, only if C2 "build"): C2 secondary `Type` and `Subject` indexes

- `map[string][]string` (sorted ID slices, using the Phase 6 helper or introducing it here if C1 did not ship) keyed on stored `Type` and on stored `Subject`, updated in `put`/`drop`. `Find` picks the smaller candidate set from `q.Type` / `q.Subject` and applies the remaining filters (`Sources`, floor, expiry, and Stage 5 `Roles`/`Where` if landed) exactly as today; with neither field set it falls back to the full order. Keys are the stored fields, never ID text (cross-stage identity note); `ErrIdentityMismatch` guarantees no re-key on `Replace`.
- Empty `Type` or `Subject` values are valid keys (empty-subject facts exist); tested via fuzz.
- If Stage 5's `find` closure for `ConflictPolicy` is landed: it uses this same path; update the index inside `put`, after the policy returns, so a re-entrant `find` sees the pre-write store. Add a test with a policy that calls `find` during `Add` and a mid-update check (S9 with a conflict-policy variant).

**Tests:** equivalence across all `Query` combinations (including `Type` only, `Subject` only, both, neither), fuzz, benchmark before/after, `-race`, `vet`, `api-check`.
**Observability & rollback:** as Phase 6. Revert one commit.
**Acceptance:** as Phase 6 for the `Find` by `Type`/`Subject`/both benchmarks.

## Phase 8 - GATED (G2, only if C3 "build"): C3 weakness-ordered eviction

- Replace the per-eviction rescan with `container/heap` keyed by (rank, confidence, `Observed`, ID ascending) using a comparator derived from `weaker`/`rank` so the order is total and identical to today's (smallest ID wins ties). The heap tracks each element's index so `put` calls `heap.Fix` on confidence change, `Source` or `Observed` change, and `drop` calls `heap.Remove` (R12). The incoming fact is inserted before eviction, so it can still be the evicted/`Rejected` one, as today.
- `Evicted` order must equal the oracle's (eviction pops in weakest-first order).
- S10: confidence changes by merge `Replace` (and `Fade`, Stage 5 `Resolution.Confidence` if landed) re-key correctly; test eviction order against the oracle after each.

**Tests:** equivalence on capped stores with many merges; dedicated S10 table; fuzz; benchmark `Add` triggering eviction before/after; `-race`, `vet`, `api-check`.
**Observability & rollback:** as Phase 6.
**Acceptance:** as Phase 6 for the eviction benchmark; S10 green.

## Phase 9 - GATED (G2, only if C4 "build"): C4 expiry-ordered `Prune`

- `container/heap` of (expiry, ID) for facts with non-nil `Expiry`, maintained in `put`/`drop` with `heap.Fix` when `Expiry` changes on `Replace`. `Prune(now)` pops while top is `<= now`, deleting through `drop`, and returns the count. Facts with nil `Expiry` are never in it.
- S11: expiry change by `Replace` then `Prune`: only expired facts removed and count equals the oracle.

**Tests:** equivalence, S11, fuzz, benchmark `Prune` before/after at the configured expired fractions, `-race`, `vet`, `api-check`.
**Observability & rollback:** as Phase 6.
**Acceptance:** as Phase 6 for the `Prune` benchmark; S11 green.

## Phase 10 - Close-out

**Why last:** Records what actually shipped so docs never describe unbuilt code (specs rule 5).
- `doc/specs/knowledge.md` section 5 and the `Store` doc comment: replace "fine for NPC-sized stores" with the measured figures and remaining limits; state per-operation complexity before/after for each shipped candidate (R19). If none shipped, the figures that justify keeping the sort (already added in Phase 4).
- `CHANGELOG.md`: per shipped candidate "internal indexing, no API change". Concurrency wording on `Store` unchanged (R18).
- Final run: `just test`, `just test-race`, `just lint`, `just api-check <start-commit>`, `just bench`; `benchmarks.md` regenerated and consistent with the code.
- Lead actions (not done by this plan): link `PRD` and `doc/design/benchmarks.md` from `doc/README.md`; mark the roadmap "Persistence seam" item "decided: no interface; revisit on trigger".

**Tests:** `docs_test.go`, leak guard, all of the above.
**Observability & rollback:** docs and changelog only.
**Acceptance:** PRD "Done when" met: green test/race/vet, empty API diff, guards green, `knowledge.md` matches the code, `benchmarks.md` current.

## Spec and doc files updated in the same change as the code

| File | Updated in | Content |
| --- | --- | --- |
| `doc/specs/knowledge.md` | Phases 2, 3, 4/10 | persistence guarantee, limits, encoding note; Persistence section; section 5 measured figures and complexity |
| `knowledge/store.go` `Store` doc comment | Phases 2, 10 | load-into-empty guarantee; measured figures replacing "fine for NPC-sized stores" |
| `doc/design/benchmarks.md` (new) | Phases 1, 4, 6-10 | results, decisions, before/after |
| `CHANGELOG.md` | Phases 1, 2, 6-9 | `just bench`; persistence contract; "internal indexing, no API change" per candidate |
| `doc/README.md`, `doc/roadmap.md` | lead action | link PRD and benchmarks doc; roadmap persistence line |

`spreading-and-inference.md`, `config.md`, `declarative.md` are not touched: Stage 4 changes no `spread`, `infer` or config behaviour.

## Security review

- **Trust boundaries:** none added. No I/O, no network, no new exported input, no new config field. Round-trip tests use in-memory values, not files.
- **Resource exhaustion:** the only new memory is index state (Phases 6-9), bounded by `O(facts)` and measured (R16); a candidate more than doubling per-fact memory for less than the approved factor is not shipped. Benchmarks at 10k facts run only under `just bench`, not in `just test`.
- **Correctness as a safety property:** a stale index silently returns wrong beliefs. Mitigations: the two-helper funnel plus source-scan guard (R11), the oracle equivalence test and fuzz (R13, S14), and shipping one candidate per change.
- **Privacy:** `All()` includes `Private` facts (existing behaviour); the Persistence section tells consumers that saves contain private beliefs.
- **Panics:** no new panic paths; index operations on missing IDs are guarded and covered by fuzz (architecture rule 12).

## Compatibility & rollout

- Exported API is byte-for-byte unchanged (R14), enforced by `just api-check` in Phases 0, 5-10. `go.mod` unchanged (R15). No data-file or config change; no migration.
- Each unit is its own change and can be reverted alone; U3 candidates are separate commits, behaviour-equivalent by test, so any can be reverted without affecting the others (C2 depends only on the shared sorted-slice helper, which moves with whichever of C1/C2 ships first).
- Stage 1 and Stage 5 interplay: Phase 5's source-scan guard fails their builds if they write the fact map directly, so landing order does not matter; whichever lands second adds its operations to the oracle and the equivalence sequences.
- Dependency failure/slowness N/A (no external dependencies).

## Decisions (made without asking; rationale)

- **D1.** Whole-stage gate G0 plus per-candidate gate G2 are modelled as explicit phases (0, 4, 5-9), not scheduled work. Rationale: roadmap says "only when measured" and PRD S13 makes "no code" a valid outcome.
- **D2.** Benchmarks live in `_test.go` files with a `TestBenchSetupsRun` guard rather than a `bench/` package. Rationale: smallest surface, no new package, setup rot caught by `just test`.
- **D3.** API check is a `just` target diffing `go doc -all` of a base ref worktree against the tree, with no committed golden file. Rationale: a golden fixture would fail when Stages 1/2/5 legitimately add API; a base-ref diff isolates this stage's change (resolves PRD A5).
- **D4.** `bench` takes an optional `count` argument (default 1, matching R5a). Rationale: noise measurement needs repeats; no extra target or tool.
- **D5.** The Persistence decision record goes in `knowledge.md`, not a new design note. Rationale: it sits beside the contract it explains and avoids a new index entry.
- **D6.** Sorted ID order (C1) is a binary-searched `[]string` with `slices.Insert/Delete`, not a tree. Rationale: stdlib only, simplest, memmove is cheap at the target sizes; shortcut limit: O(n) insert, upgrade only if Phase 6 evidence shows it. C2 reuses it.
- **D7.** No `benchstat`/external tool; comparison by median and recorded noise margin. Rationale: R15 and "ask before adding dependencies".
- **D8.** Persistence into a store with a Stage 5 `ConflictPolicy` is documented as not guaranteed, no new API (PRD recommendation (a), A4).
- **D9.** The two mutation helpers are named `put` and `drop` and take only a `Fact` / ID. Rationale: minimal, enough for insert/replace/confidence/expiry/delete; names are unexported so can change freely.
- **D10.** Benchmark grid in P-A1 is a measurement grid, not a target; thresholds stay with the user at G1.

## Cross-stage assumptions (exact names)

- Stage 1: `knowledge.Store.Fade(now Tick) FadeResult` (`Changed`, `Forgotten []string`), `WithFader(Fader) Option`, `Fader func(f Fact, now, elapsed Tick) (confidence float64, forget bool)`, `Fact.FadedAt Tick`, `Config.Fade FadeConfig` (`HalfLives`, `DefaultHalfLife`, `ForgetBelow`); `spread.Transform func(e Encounter, delivered knowledge.Fact) knowledge.Fact`, `Encounter{Teller, Listener Holder; Fact knowledge.Fact; Now knowledge.Tick}`. `Fade` mutates only through `put`/`drop`; `FadedAt` round-trips as stored.
- Stage 5: `knowledge.WithConflictPolicy(ConflictPolicy)`, `ConflictPolicy func(incoming Fact, find func(Query) []Fact, rank func(source string) int) Resolution`, `Resolution{Reject bool; Remove []string; Confidence map[string]float64}`, `AddResult.Conflicted []string`, `ErrInvalidResolution`, `Fact.Roles map[string]string` (deep-copied by `clone`), `Query.Roles`, `Query.Where func(Fact) bool`. `Add` order: validate, clamp, identity, merge decision, conflict policy, write, `MaxFacts` eviction (preserved).
- Stage 2/3: `Fact` identity stays `Type`, `Subject`, `Discriminator` or explicit `ID`; `declare.Catalog` untouched; Stage 3's second-consumer task is not a dependency.
- Stage 4 itself adds no exported identifier.
