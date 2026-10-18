# Plan: Stage 5, deeper semantics

**Status: Proposed.** Sequences [prd-stage5-deeper-semantics](../specs/prd-stage5-deeper-semantics.md) (units A, B, C) into gated phases. Built against `doc/rules/architecture.md` (incl. "Idiomatic Go"), `doc/rules/testing.md` and `doc/rules/specs.md`. No `doc/plans/` convention existed; this file sets the format (one `## Phase` per buildable chunk, each with Why first / Deliverables / Tests / Observability & rollback / Acceptance). A plan decides nothing: every behaviour below is fixed by the PRD, the code, or a numbered entry in "Decisions".

## Gating model (read first)

Stage 5 is demand-driven (roadmap: "only on demand"; PRD section 1 "Stage gate"). Nothing here is scheduled work. Each phase has a **Gate** line; if its gate is not met the phase is not started, and not starting is a successful outcome (the PRD stays a design record).

| Phase | Gate |
| --- | --- |
| 1 Gate record | none (it is the check) |
| 2 Unit A | a named consumer need for absence in inference, recorded in Phase 1's gate record |
| 3 Unit B | a named consumer need for contradiction handling, recorded in Phase 1's gate record |
| 4 Unit C1 | consumer need recorded, AND Stage 3 multi-party recipe tried on it and the awkwardness written down (in the recipe's `limits` paragraph in `doc/recipes.md` or `doc/design/second-consumer.md`) (PRD D5, Unit C "gated twice") |
| 5 Unit C2-C4 | Phase 4 shipped, AND the gate record states why `Query.Where` is not enough |

A and B are independent; either order, or parallel on separate branches (they touch different files except the shared `knowledge.AddResult` and spec text, so merge A first if sequential).

## Scope

**In scope.** PRD units A (`infer.Requirement.Absent`), B (`knowledge.WithConflictPolicy`, `ConflictPolicy`, `Resolution`, `ErrInvalidResolution`, `AddResult.Conflicted`), C1 (`Query.Where`), C2-C4 (`Fact.Roles`, `Query.Roles`, `ArgNames.Roles`, roles arg on `knows` and `add_fact`). Spec, README, CHANGELOG and example updates that go with each.

**Out of scope** (all PRD section 2; tracked nowhere because rejected or deferred by decision): retraction (D2, `TTL` is the mitigation); multi-variable joins in inference and roles in `infer` (C5); age/window conditions; built-in conflict notions; a data-file form of conflict policy (D3, revisit when a consumer asks); a shipped `StrongerWins` helper (D4); probabilistic belief merging, schedulers, event bus. Persistence and indexing are Stage 4 (see Cross-stage assumptions).

## Assumptions this plan makes (flagged for override, sequencing-level only)

- S1. Units are built A then B then C if all are triggered; nothing depends on the order.
- S2. Each unit is its own change (branch / PR), carrying its own spec, code, tests, README and CHANGELOG edits, so a gated-out unit never leaves residue.
- S3. The gate record lives in the spec text being amended for that unit (a "Consumer need" paragraph in the relevant spec section), because `specs.md` already requires the user to approve that text before code. No new file type is invented.
- S4. The Stage 1, 2 and 4 code may or may not have landed when a unit starts. Each phase's first deliverable is "re-read the current `Add`/`ArgNames`/mutation helpers and adapt"; the plan names the interface it needs, not line numbers.

## Decisions (made by the plan author under the lead's principles; PRD decisions D1-D6 are honoured, not re-litigated)

- P1. **Spec first, then `Example_`, then code.** `specs.md` rule 1 and "Idiomatic Go" rule 1. Each unit's first two deliverables are the spec amendment (user-approved) and the failing `Example_` test; API is adjusted until the example reads well before implementation. Rationale: repo rules, and it keeps the exported surface minimal.
- P2. **Unit A needs no `declare` code.** `declare` decodes `inference:` straight into `infer.Rule` with `DisallowUnknownFields` (`declare/document.go`), so a `json:"absent,omitempty"` / `yaml` tag on `Requirement.Absent` makes the key loadable and a non-bool a decode error (A8). Rationale: smallest change; only `declarative.md` text and a `FuzzLoad` seed change.
- P3. **`Resolution` validation runs against the pre-application store, all-or-nothing.** B5 says "change nothing"; checking before any mutation is the only way to guarantee it. Rationale: errors are values, no partial writes.
- P4. **An ID listed in both `Remove` and `Confidence` is `ErrInvalidResolution`.** The PRD is silent; applying Remove then Confidence would name a no-longer-stored fact (B5 itself calls that invalid), so rejecting is the consistent reading. Rationale: no silent precedence rule to remember.
- P5. **`Confidence` keyed by the incoming ID is allowed and sets the incoming fact's confidence before the write; combined with `Reject` it has no effect.** Valid so a policy can weaken a newcomer without rejecting it. Rationale: B3/B4 already name the incoming ID as legal.
- P6. **`Conflicted` lists held IDs only (never the incoming ID), deduplicated, ascending.** Matches B7 wording "held facts".
- P7. **The `find` closure is `func(q Query) []Fact { return s.Find(q, incoming.Observed) }` filtered to drop `incoming.ID`, evaluated before any mutation.** So on a same-ID `Replaced` path the soon-to-be-overwritten fact is not shown (B3). Rationale: D1 plus Stage 4's requirement that the index not be mid-update during the policy call.
- P8. **All Store-internal mutations introduced here go through one small unexported helper (`applyResolution`) that uses the same delete/write primitives as `Prune`/`enforceCap`**, or Stage 4's two mutation helpers if they exist (S4). Rationale: Stage 4 R11 obligation, costs nothing now.
- P9. **`Where` is evaluated last, only on facts that passed every other filter, on the already-cloned fact.** Cheapest, and the caller cannot mutate stored state through it beyond the aliasing `Payload` already allows. `Where` and `Roles` on `Query` apply to `Find` only (the conflict `find` closure is `Find`, so it gets them for free); `All`/`Get` stay raw.
- P10. **Roles copy semantics:** `clone` deep-copies `Roles` (`maps.Clone`); a nil and an empty map are both "no roles" and neither is ever written back as non-nil from `clone` of nil. Rationale: round-trip equality for Stage 4 R6.
- P11. **`Roles` are excluded from the merge decision and from ID derivation; on `Replace` the incoming fact's roles replace the held ones** (the whole fact is replaced today). Rationale: SC3 says roles are not identity; replacing whole facts is existing behaviour.
- P12. **The roles arg is parsed by a new unexported helper `stringMap(a Args, key string)` in `declare/knowledgeops.go`**, beside the other arg parsing; `Args` gains no exported method. It accepts a string-to-string object, anything else is `ErrBadArgs`. Rationale: smallest exported surface.
- P13. **No new tunables except `ArgNames.Roles` (Phase 5), so only Phase 5 adds a default and its two default tests (testing rule 4); Phases 2-4 add none and `config.md`'s tunables table is unchanged.** Rationale: PRD section 4.
- P14. **Persistence into a policy-equipped store is documented as not guaranteed** (option (a) recommended by Stage 4). Rationale: no new API, and the policy is construction-time.

## Phase 1: Gate record (no code)

**Gate:** none. **Why first:** every other phase is conditional on it; doing it first prevents speculative work.

- Ask whether a named consumer need exists for each unit (A absence, B contradictions, C relational). Stage 3's findings record (`doc/design/second-consumer.md`) and the `limits` paragraph of the multi-party recipe in `doc/recipes.md` are the evidence sources for C.
- For each unit that has a need, write the "Consumer need" paragraph into the spec section it will amend (S3) and get that spec change approved by the user (`specs.md` rule 1).
- If no unit has a need: stop and change nothing.

**Tests:** none (docs only). **Observability & rollback:** N/A, documentation. Revert the paragraph. **Acceptance:** per-unit gate status is written down; only units with a recorded need proceed.

## Phase 2: Unit A, absence in inference

**Gate:** Phase 1 recorded a need for A. **Why first among code phases:** lowest risk, additive pure data semantics, no dependency, and it proves the gate-to-example workflow cheaply.

Deliverables, in order:
1. **Spec** (user-approved): amend `doc/specs/spreading-and-inference.md` (B-section: `Requirement.Absent`, matching, A5 validation error, pass-snapshot semantics A6/A7, no-retraction note D2) and `doc/specs/declarative.md` (`inference:` accepts `absent`). `doc/specs/config.md`'s document grammar gets the key. Update `doc/specs/overview.md` (the "Negation / absence in inference" out-of-scope bullet, line 94) so it no longer claims absence is unsupported. `CHANGELOG.md` "Notes" drops "negation in inference" from the out-of-scope list.
2. **`Example_` first** (`infer/example_test.go`, office gossip, PRD section 8): hiring freeze with no job postings gives `team_shrinking`. Also add `absent` to the office or a new `declare/testdata/` JSON used by the integration test (SA6 golden).
3. **Code** in `infer/infer.go`:
   - `Requirement.Absent bool` with `json:"absent,omitempty" yaml:"absent,omitempty"` and doc comment (extends the existing struct, no new type).
   - `New`: A5 validation added next to the existing `allFixed` check, reusing `bad(...)` and `ErrInvalidRule`. Needed predicates: `hasUnboundPositive`, `allFixed` (absent requirements count as fixed/unbound by their `Subject` exactly like positive ones, so the existing all-fixed rule is unchanged).
   - `fire`: reuse the existing `best(q)` (same `store.Find` call, so A2's "one visibility notion" holds by construction). For an absent requirement: fixed subject, if `best(q)` is non-empty return nil, else continue, with no premise; unbound, record it and after `candidates` are computed drop each candidate `s` for which `store.Find(Query{Type, Subject: s, Sources, MinConfidence}, now)` is non-empty. Absent requirements add nothing to `premises` or `unbound` (A4). `candidates` is computed only from positive unbound requirements, so the empty-`unbound` branch still yields `[]string{r.Produces.Subject}`.
   - No change to `Run`: it already evaluates each pass from the store at pass start (A6) and later passes see earlier-pass facts (A7).
4. **`FuzzLoad` seeds** (`declare/fuzz_test.go`): add a valid `absent: true` document and an `"absent": "yes"` document.
5. **Docs/records:** `README.md` snippet of the absence rule (neutral domain), `CHANGELOG.md` "Added" (and note unkeyed `Requirement` literals break), `doc/README.md` spec list unchanged except its status marks, roadmap/spec "implemented" markers as repo practice dictates.

**Tests** (testing.md: table-driven, hermetic, seeded/injected tick):
- Unit, `infer/infer_test.go`, one table: SA1 (fires, premises list only the freeze fact), SA2 (present above floor suppresses), SA3 (below floor counts absent), SA4 (`ErrInvalidRule` for unbound absent with no unbound positive; also all-absent unbound; also all-absent fixed with `Produces.Subject` valid, without invalid), SA5 (two mutually-absent rules both fire in pass 1; `max_passes: 2` third rule does not fire in pass 2; repeated runs identical), fixed-subject absent independent of bound `s`, `Sources` filter, expired fact counts as absent.
- Integration, `declare` test: SA6 golden (existing documents produce identical IDs, order and `Passes`; capture golden from current `main` before changing `fire`) and loading a data file with `absent`.
- Unit + fuzz seed: SA7 decode error, no panic.
- Example: SA1 as runnable doc.
- Pyramid: all pure in-memory, so unit-heavy by nature; one integration (data-file) layer and one example per the PRD table. No e2e layer exists in this library.

**Observability & rollback:** library, no logging; observable surface is `infer.Result`. Rollback is `git revert` of the unit's change; because the change is additive and the key is omitted by default, no data migration. Pre-merge safety is the SA6 golden.

**Acceptance:** SA1-SA7 pass; the office example is a runnable `Example_` using `absent` with no Go beyond existing wiring; `go test ./...`, `go test -race ./...`, `go vet ./...` clean; `TestNoDomainVocabulary` and `docs_test.go` pass; specs updated in the same change.

## Phase 3: Unit B, contradictions

**Gate:** Phase 1 recorded a need for B. **Why after A:** same quality bar but medium risk (new hook inside `Add`, new result field, eviction interplay). Independent of A; Phase 2 is not a code dependency.

Deliverables, in order:
1. **Spec** (user-approved): `doc/specs/knowledge.md` (API: `ConflictPolicy`, `Resolution`, `WithConflictPolicy`, `AddResult.Conflicted`, `ErrInvalidResolution`; 3.1 Add pipeline order validate, clamp, identity, merge decision, conflict policy, write, eviction; D1 lookup-tick note incl. the `Observed: 0` caveat; B8 no-reentry contract; P14 persistence note; worked spaceport example). `doc/specs/spreading-and-inference.md` (spread/infer deliveries and derived facts are subject to a listener's/holder's policy, B9; `spread.Transfer` unchanged). `doc/specs/overview.md` (the "Belief revision" bullet, line 93, now points to the conflict hook); `CHANGELOG.md` "Notes" drops "contradiction handling" from the out-of-scope list. `doc/specs/config.md`: state "no new tunables". Error-cases table gains `ErrInvalidResolution`.
2. **`Example_` first** (`knowledge/example_test.go`, spaceport berths): the custom stronger-wins policy (SB1), then the re-weighting policy (SB3). If the example reads clearly worse than a helper would, stop and raise it (B10 "revisit before implementation") rather than adding a helper silently.
3. **Code** in `knowledge/`:
   - `fact.go`: `AddResult.Conflicted []string`; `ErrInvalidResolution` sentinel (beside the existing sentinels); types `ConflictPolicy` and `Resolution` exactly as PRD section 9 item 2.
   - `store.go`: `conflict ConflictPolicy` field; `WithConflictPolicy(p)` option mirroring `WithMergePolicy` (nil ignored, so unset stays zero-cost, B1). In `Add`, immediately before each of the two write sites (the `!ok` insert and the `Replace` branch) call one unexported `s.resolve(f)` that is a no-op returning immediately when `s.conflict == nil`. The merge-`Keep` path never calls it (B2/SB5).
   - `resolve`: build `find` (P7), call the policy, validate everything (P3, P4, P5, B5: unknown IDs, NaN, outside `[0,1]`, incoming ID in `Remove`), return a plan; then apply in the B6 order (Remove ascending, Confidence ascending, incoming write unless `Reject`), report `Conflicted` (P6), set `Outcome = Rejected` on `Reject`, then `enforceCap` as today (an incoming evicted by cap is `Rejected`, unchanged). Apply held-fact mutations only through the P8 helper. `FadedAt` (Stage 1) and every field other than `Confidence` are left untouched on re-weighting.
   - Policy panics propagate (no recover).
4. **Docs/records:** README snippet, CHANGELOG (additive; unkeyed `AddResult` literals break), doc comments with concurrency statement ("`Store` is not safe for concurrent use; the policy runs inside `Add` and must not call back into the store").

**Tests:**
- Unit, `knowledge/store_test.go`, table-driven with fakes (plain funcs, no mocks): SB1, SB2, SB3, SB4 (unknown Remove ID; unknown Confidence ID; NaN; 1.4; incoming ID in Remove; same ID in Remove and Confidence; each leaves `All()` identical and returns `errors.Is(err, ErrInvalidResolution)`), SB5 (policy not invoked on merge `Keep`; counter), SB6 (cap + removal means no eviction; cap without removal evicts as today), P5 cases, D1 (lookup at `incoming.Observed`: an expiring held fact is visible at the tick before expiry and not at it; a direct `Add` with `Observed: 0`), P7 (the same-ID replaced fact is not in `find`), idempotent re-add with policy.
- SB8: the entire existing knowledge, spread, infer and declare suites run unmodified (no policy); add an explicit test that two stores, one with `WithConflictPolicy(nil)`, behave identically over a scripted `Add` sequence.
- Integration (`spread` test, `infer` test): SB7 spread delivery into a policy-equipped listener (`Transfer.Outcome` reflects result, no `spread` change) and an inferred fact subject to the policy (B9). Both live in the existing test packages and use only exported API.
- SB9: single-goroutine run under an external mutex, `go test -race`, documenting the contract. Determinism: run each table twice and compare; no RNG.
- Pyramid: unit-heavy on the hook semantics, two integration tests for B9, examples for docs.

**Observability & rollback:** surface is `AddResult.Conflicted` and `Outcome`; no logging. Rollback is revert; additive option, default nil. The pre-merge safety net is SB8 (unused path unchanged).

**Acceptance:** SB1-SB9 pass; the spaceport example resolves with the stronger-wins policy and again with the re-weighting policy; B9 tests pass without any edit to `spread` or `infer` non-test code; `go test -race`, `go vet`, leak guard and docs tests clean; specs updated in the same change.

## Phase 4: Unit C1, `Query.Where` (double-gated)

**Gate:** need recorded AND Stage 3 recipe evidence written (S3). **Why after B:** it is the cheapest relational step, and a conflict policy's `find` benefits from it automatically (P9). It may be the end of Unit C.

1. **Spec** (user-approved): `knowledge.md` Query section and 3.4: `Where func(Fact) bool`, ANDed, nil means no filter, not loadable from data, `Payload` still never inspected by the library; include the recorded awkwardness.
2. **`Example_` first** (hospital-ward handover, SC1).
3. **Code:** `Query.Where` field with doc comment; in `Find`, after the existing exclusions, `q.Where != nil && !q.Where(cloned)` continues. No other callers change (`infer` and `declare` build `Query` with keyed fields).
4. CHANGELOG; README note.

**Tests:** SC1 (example + unit: only that nurse's handovers), SC2 (nil `Where` behaves as before, existing suite), `Where` combined with every other field, `Where` not called for non-matching or expired facts (counter), `Where` seeing a clone. Unit level is right: pure function of store state.

**Observability & rollback:** none needed (pure query); revert the field. **Acceptance:** SC1-SC2 pass, gate suites clean, spec updated in the same change. If the recorded evidence says `Where` suffices, **stop here**; Phase 5 is not started.

## Phase 5: Unit C2-C4, `Roles` (triple-gated, provisional)

**Gate:** Phase 4 shipped AND the gate record shows data-driven rules need to see parties and `Where` cannot (PRD D5). The design stays provisional until then; re-check the section 9 interfaces against whatever Stages 1, 2, 4 have shipped.

1. **Spec** (user-approved): `knowledge.md` (`Fact.Roles`, `Query.Roles`, not identity, P10/P11), `declarative.md` (roles arg on `knows`/`add_fact`, `ArgNames.Roles`, `ErrBadArgs`/`ErrBadBindings`), `spreading-and-inference.md` (D6: a `Transform` may change `Roles`, like `Payload`; the copied-back fields list gains `Roles`, identity fields stay restored; filters see `Roles` through `Encounter.Fact`; C5, `infer` unchanged).
2. **`Example_` first** (ward handover with `Roles{from,to}`).
3. **Code:**
   - `knowledge`: `Fact.Roles map[string]string` (`json:"roles,omitempty"`), `Query.Roles`, exact-match check in `Find`, `clone` deep copy (P10), not in ID derivation (so U+001F check and `per_event` unaffected).
   - `declare`: add field `Roles string` to Stage 2's `ArgNames` (`json:"roles"`, zero = default `roles`; `DefaultArgNames()` gains `roles`); extend Stage 2's single unexported allow-list helper for `knows` and `add_fact` (union rule R3.9), never edit `Unknown(...)` calls directly; parse the arg (P12); a rename colliding with another arg of the same action is `ErrBadBindings` at registration (same check Stage 2 added); mistyped value is `ErrBadArgs`.
   - `spread`: in `delivered()` copy `Roles` back from the `Transform` result (deep-copied with `maps.Clone`) alongside Stage 1's `Confidence`, `Payload`, `Expiry` copy-back (PRD D6); add a test that a `Transform` can alter `Roles` while ID fields stay restored.
   - `infer`: none (C5).
4. CHANGELOG, README; extend Stage 4's round-trip test (R6) to compare `Roles` if Stage 4 landed.

**Tests:** SC3 (query by role; two facts differing only in roles share one ID and merge by normal policy; replace carries incoming roles), SC4 (`add_fact` with roles, mistyped arg `ErrBadArgs`, `ArgNames.Roles` rename works and the default name is then unknown, collision is `ErrBadBindings`, zero `ArgNames` uses default), `clone` aliasing test (mutating a returned fact's `Roles` does not affect the store), `Transform` may alter `Roles` while ID fields stay restored, `DefaultArgNames` value test plus override test (testing rule 4, since this adds a default), `FuzzLoad` seed with `roles`, existing suites unchanged.

**Observability & rollback:** none beyond result types; additive, `omitempty`, revert-safe, data files without `roles` unchanged. **Acceptance:** SC3-SC4 pass, all suites and leak guard clean, specs updated in the same change, and no change in any package outside `knowledge` and `declare` (non-test).

## Security review

No I/O, network, files, secrets or auth are introduced. Trust boundaries: (1) **data files** gain two optional keys (`absent`, `roles`): both go through the existing strict decoder (unknown field and wrong type are errors, covered by `FuzzLoad` seeds), and a malformed value cannot panic. (2) **Caller-supplied functions** (`ConflictPolicy`, `Where`) run in the caller's trust domain on the caller's goroutine; panics propagate as for existing filters and transforms, documented. A policy returning invalid IDs or confidences is contained by P3/B5 validation, so it cannot corrupt the store. (3) **Resource use:** a policy can call `find` repeatedly (each O(n log n)); documented as the policy author's cost. Absent requirements add one extra `Find` per candidate subject, bounded by rules x candidates. (4) **Privacy:** the policy sees only its own holder's store (including that holder's `Private` facts); no new data leaves a holder; `Resolution` cannot make a private fact public. (5) **Re-entrancy:** a policy that captures the `*Store` and calls `Add` is undefined by documentation (B8); the `find` closure is read-only by construction.

## Compatibility & rollout

All changes are additive on `v0.x` (PRD section 4): new option, new struct fields, a new optional data key, one new sentinel, one new `ArgNames` field. Keyed literals are unaffected; unkeyed `AddResult`/`Requirement`/`Fact`/`Query`/`ArgNames` literals would break, stated in `CHANGELOG.md`. Existing documents and call sites behave byte-for-byte as before (SA6 golden, SB8 unused-path test, zero-value `ArgNames`). Each unit ships as its own change so a gated-out or reverted unit leaves nothing behind. No flag or expand-contract is needed. There is no remote dependency to fail. Rollout of the library itself is a tag; Stage 5 does not affect the v0.1.0 tag gate.

## Cross-stage assumptions

- **Stage 1:** `Fact.FadedAt`, `Store.Fade(now) FadeResult`, `WithFader(Fader)`, `knowledge.Config.Fade`. Conflict `Remove`/`Confidence` never touches `FadedAt`; `Fade` never runs the conflict policy; the two are separate paths. If `Add` is rewritten, the order validate, clamp, identity, merge, conflict, write, eviction is preserved. `spread.Transform` may be widened to take an `Encounter`; Stage 5 needs no change to it.
- **Stage 2:** `declare.ArgNames`, `DefaultArgNames()`, `Bindings.ArgNames`, a single unexported allow-list helper per action (union rule R3.9), `ErrBadBindings` on arg-name collision, `add_fact` `discriminator`/`discriminator_from` args (makes Unit B reachable from data rules). If attribute matching reaches `infer.Requirement`, `Absent` negates the whole match.
- **Stage 3:** the multi-party recipe (`doc/recipes.md`, `limits` paragraph) and `doc/design/second-consumer.md` are the evidence for Phases 4-5; no code dependency. **Stage 1:** Phase 5 changes `spread.delivered()` to copy `Roles` back from the `Transform` result (Stage 1 P-D3 lists Confidence, Payload, Expiry only).
- **Stage 4:** no new exported API there; if indexing exists, all mutations go through its two unexported mutation helpers (P8), the policy call precedes index update, `Find` results are index-independent, `Where`/`Roles` filter the narrowed set, and the round-trip contract (R6) is stated for a store without a conflict policy (P14); `AddResult.Conflicted` is compared by the equivalence oracle.
