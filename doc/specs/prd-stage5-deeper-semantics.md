# Stage 5: deeper semantics PRD

Status: draft · Owner: rumour_mill maintainers (drafted for the lead agent to reconcile) · Last updated: 2026-10-18

Source: `doc/roadmap.md`, "Stage 5: deeper semantics, only on demand". Builds on [knowledge](knowledge.md), [spreading-and-inference](spreading-and-inference.md), [declarative](declarative.md), [config](config.md). Conventions followed: `doc/rules/architecture.md` (including "Idiomatic Go"), `doc/rules/testing.md`, `doc/rules/specs.md`. No UI surface, so the UX section is N/A. There is no design system to check.

Tags below: **[decided]** is fixed by a spec, the code, a rule file or a lead decision. Section 10 records every decision, including the ones that changed from the first draft. No open questions remain.

## 1. Problem & Goal

**End user.** The developer integrating `rumour_mill` into a game or simulation (the "consumer"). Their players only benefit indirectly, through NPCs that behave more believably.

**What the consumer cannot do today** (verified against the code and `overview.md` section 6):

1. **Contradictions.** Two incompatible claims are simply two facts. If an NPC hears "the freighter is in bay 4" and later "bay 7", both sit in the store and both are visible to `Find`. `MergePolicy` only ever sees two facts with the *same ID*, so it cannot compare differently-identified claims. The consumer must scan the store after every `Add`, outside the library, and has no way to make the same resolution apply to facts arriving by spreading or inference.
2. **Absence in inference.** `infer.Requirement` tests only for presence. "Knows a hiring freeze, has *not* seen any job postings, therefore suspects the team is shrinking" is inexpressible in a data file. (Rules already have absence: `{use: not, of: [{use: knows, ...}]}` works today. Only inference lacks it.)
3. **Relational claims.** A claim with several parties ("nurse A handed patient P over to nurse B") must be flattened into `Subject` + `Discriminator` strings or an opaque `Payload`. The library cannot query "claims involving B" and data-driven rules cannot see inside `Payload` **[decided]**.

**Goal.** For each gap, give the consumer the smallest hook or data field that closes it, so the consumer writes policy (small Go functions or data) and the library stays domain-free, deterministic and small.

**Stage gate [decided, roadmap].** Nothing here is built speculatively. Each unit (section 5) starts only when a named consumer need exists, and each ends with a worked example drawn from that need. If no consumer asks, this PRD stays a design record and no code is written.

**Success measure.** No usage data exists (v0.x, no second consumer yet). Success is therefore checkable by construction: for each unit, the consumer's worked example (section 8) is expressed with the new feature and **no change to any package outside the unit's own** (the same "generality proof" standard as the hospital-ward test in `declarative.md` section 7), and the behaviour with the feature unused is byte-for-byte unchanged.

## 2. Scope

### In scope
- Unit A: an `absent` form of `infer.Requirement`.
- Unit B: a caller-supplied conflict hook on `knowledge.Store`.
- Unit C: a decision on first-class relational facts, with a minimal design if the gate is met.

### Out of scope
- **Retraction.** Derived facts still do not track their premises; a derived fact outlives a premise that later changes (existing behaviour, `spreading-and-inference.md` B3). Absence makes this more visible; the mitigation is `TTL`, not a new truth-maintenance system.
- **Multi-variable joins in inference** (binding two subjects at once). Unit C stops at storage and querying.
- **Age or window conditions** ("not seen in the last N ticks"). Neither positive nor absent requirements have them today.
- **Built-in domain notions of conflict** (what makes two facts incompatible is always caller policy).
- **A declarative (data-file) form of conflict policy.** Deferred until a consumer needs it; see decision D3.
- Probabilistic belief merging, Bayesian updates, any scheduler or event bus (roadmap "Not planned").

### Priority (by consumer value for effort)
Must (if the stage is triggered at all): A, then B. Could: C, which is gated again on Stage 3 recipe evidence.

## 3. Requirements

Each is one behaviour. "Today" cites the current behaviour so the delta is clear.

### Unit A: absence in inference

- **A1.** `infer.Requirement` gains `Absent bool` (`json:"absent,omitempty"`). When true, the requirement is satisfied when **no** fact matches its `Type`, `Subject`, `MinConfidence` and `Sources`. A rule fires only if every requirement is satisfied.
- **A2.** "Matches" for an absent requirement uses exactly the positive matching test (`B3`): unexpired at `now`, confidence at or above the requirement's floor (nil = store query floor), source in `Sources` if given. A fact below the floor therefore counts as absent. *Why: a consumer reasons "I have not heard it credibly", and one rule means one visibility notion.*
- **A3.** An absent requirement with empty `Subject` uses the rule's bound subject `s`: the rule fires for `s` only if no matching fact about `s` exists. An absent requirement with a fixed `Subject` checks that subject, independent of `s`.
- **A4.** An absent requirement does not contribute candidate subjects and does not contribute a premise ID to `Prov.Premises`.
- **A5.** `infer.New` rejects with `ErrInvalidRule` a rule that has an unbound (empty-`Subject`) absent requirement but no unbound positive requirement to bind `s` (nothing would supply candidates). An all-fixed rule is already valid only when `Produces.Subject` is set; absent fixed requirements do not change that. A rule whose *every* requirement is absent is valid only if all are fixed and `Produces.Subject` is set.
- **A6.** Evaluation stays snapshot-based per pass: all rules in a pass see the same snapshot, so rule order does not matter and mutually-absent rules (rule 1 needs no `X`, rule 2 needs no `Y`, each producing the other) both fire in the same pass.
- **A7.** With `max_passes > 1`, an absent requirement in pass *n* sees facts derived in earlier passes (they are in the store). This is documented, and a table test pins it.
- **A8.** `absent` is accepted in the `inference:` document section; `declare.Load` rejects `absent` with a non-bool as a decode error, and `FuzzLoad` seeds include it.
- **A9.** A document without `absent` produces identical derived facts, IDs, order and `Result.Passes` as before.

### Unit B: contradictions

- **B1.** `knowledge.WithConflictPolicy(p ConflictPolicy)` is a store option. Without it, `Add` behaves exactly as today (**[decided]**: zero cost, zero behaviour change).
- **B2.** The policy runs inside `Add`, after the merge decision, and only when the incoming fact would be stored (merge outcome `Inserted` or `Replaced`). A fact the merge policy already rejects never reaches the conflict policy.
- **B3.** The policy receives the incoming fact, a read-only lookup over the store (`func(Query) []Fact`, same semantics as `Find` at tick `incoming.Observed`, excluding `incoming.ID`), and the store's source-rank function. *Why: the lookup lets the consumer define "related" any way they like (same type and subject, a different type, a role), instead of the library guessing a key. Rank lets resolution respect `source_ranks` without re-reading config.*
- **B4.** The policy returns a `Resolution` that can: reject the incoming fact (`Reject`), remove held facts by ID (`Remove`), and set a new confidence for held facts or the incoming fact (`Confidence`, keyed by ID).
- **B5.** The resolution is validated before anything is applied. A `Remove` or `Confidence` ID that names no stored fact (other than the incoming ID for `Confidence`), a NaN or out-of-range confidence, or an attempt to `Remove` the incoming ID makes `Add` return `ErrInvalidResolution` and change nothing. *(Clamping, as `Add` does for fact confidence, is rejected here: a policy returning 1.4 is a bug to surface.)*
- **B6.** On a valid resolution, application order is fixed: `Remove` in ascending ID, then `Confidence` in ascending ID, then (if not `Reject`) the incoming fact is written, then `MaxFacts` eviction runs as today. If `Reject` is set the incoming fact is not written and `AddResult.Outcome` is `Rejected`, but `Remove` and `Confidence` entries for held facts are still applied (a policy may reject a newcomer yet weaken a held fact).
- **B7.** `AddResult` gains `Conflicted []string`: IDs of held facts removed or re-weighted by the policy, ascending, empty when none.
- **B8.** The lookup closure is read-only. The policy must not call `Add` on the same store; this is documented as a contract. The closure cannot write, so the contract is enforced by construction for the closure and by documentation for a captured `*Store`.
- **B9.** Because spreading and inference deliver through `Store.Add`, a listener's or inferring holder's conflict policy applies to those facts with no change to `spread` or `infer`.
- **B10.** No policy helper is shipped: the library exports the hook and types only (smaller surface, lead decision). The spaceport example carries a short custom policy; if that example genuinely reads worse than with a helper, revisit before implementation.

### Unit C: relational claims (gated twice)

Gate 1: a consumer need exists. Gate 2: the Stage 3 recipes for multi-party claims (encode parties in `Discriminator`/`Payload`) have been tried on that need and found awkward, with the specific awkwardness recorded in the spec. Only then:

- **C1.** (Should, cheapest step) `Query` gains `Where func(Fact) bool`, ANDed with the other fields. This lets a consumer filter on `Payload` without the library inspecting it, so the "never inspected" rule on `Payload` stays true. Not available from data files.
- **C2.** (Could, only if C1 proves insufficient because data-driven rules need to see the parties) `Fact` gains `Roles map[string]string`: caller-chosen role names to holder or subject IDs (for example `from`, `to`). Roles are data attached to the fact, **not** part of identity, and copied on the way in and out of the store like `Prov.Premises`.
- **C3.** (with C2) `Query` gains `Roles map[string]string`: every listed role must match exactly.
- **C4.** (with C2) `knows` and `add_fact` accept a roles arg, default name `roles`, named through `Bindings.ArgNames.Roles` (an additive field on Stage 2's `ArgNames`) and added to the `Unknown(...)` allow-list through Stage 2's single helper (union rule).
- **C5.** (with C2) Inference requirements and products are **not** extended to roles in this stage (no joins).

## 4. Non-Functional Requirements

**Determinism [decided, arch rule 11].** Conflict resolution is applied in a fixed order (B6); candidate and premise ordering in inference is unchanged. No RNG, no clock: the lookup tick is `incoming.Observed`. Decision D1: `Observed` is the lookup tick. It is a reliable "now" because spreading, `add_fact` and inference all set it to the current tick; a direct `Add` with `Observed: 0` gets lookup-at-tick-0 and the docs say so.

**Concurrency [decided, arch rule 10].** `Store` stays not safe for concurrent use; the policy runs under the caller's lock, inside `Add`. The policy must not re-enter the store (B8). `infer.Engine` stays immutable.

**Performance.** No benchmark gate (Stage 4 owns measurement). Complexity is stated: the conflict policy is invoked once per qualifying `Add`; each lookup call costs a `Find` (O(n log n) today). Units must not slow the unused path: with no policy and no `absent` the code path adds only a nil check.

**Errors [decided, arch rule 12].** New sentinel `knowledge.ErrInvalidResolution`, wrapped with `%w`; `infer.ErrInvalidRule` reused for A5. Policy panics propagate (consistent with filters and transforms).

**Security / privacy.** None added: no I/O, no network. `Private` facts: a policy may see private facts of its own holder (it is the holder's own store); nothing here changes what leaves a holder.

**Configuration [decided, arch rule 2; testing rule 4].** This stage adds **no tunables**: confidence factors and thresholds live in the consumer's policy code or data. Therefore no new config default needs a default-value test. If review changes that, any new field gets a `DefaultConfig` entry and the two tests rule 4 demands.

**Observability.** A library: the observable surface is `AddResult.Conflicted` (B7) and `infer.Result`. No logging.

**Rollout and compatibility [decided, v0.x].** All changes are additive: new option, new struct fields, new optional data key. No existing signature changes, so no deprecation. `knowledge.AddResult` and `infer.Requirement` gain fields; callers using keyed literals are unaffected, unkeyed literals would break (documented in `CHANGELOG.md`). Because the leak guard and `docs_test.go` run on every change, new exported identifiers must be documented.

## 5. UX & Design

N/A. This is a Go library with no user-facing surface. Developer experience is covered by the usage-first rule (`architecture.md`, "Idiomatic Go" rule 1): each unit's `Example_` test and README snippet are written before the implementation and the API adjusted until they read well.

## 6. Stakeholders & Acceptance

- **Owner:** rumour_mill maintainers. **Approver:** the user (spec rule 1: spec approved before code). **Reconciler:** the lead agent, for cross-stage interfaces (section 9).
- **Done for the stage:** every started unit satisfies its criterion below, plus `go test ./...`, `go test -race ./...`, `go vet ./...` clean and the leak guard passing (testing rules 7-9), and the corresponding spec files (`knowledge.md`, `spreading-and-inference.md`, `declarative.md`, `config.md` as touched) updated in the same change (spec rule 5).

## 7. Phases / Units

| Unit | Depends on | Risk | Decisions | Acceptance criterion |
| --- | --- | --- | --- | --- |
| **A** absence in inference | none | Low. Additive, pure data semantics. Subtle point: non-monotonic results across passes (A6/A7), pinned by tests. | Decided: reuse positive matching (A2). Decided: no retraction (D2). | Scenarios SA1-SA7 pass; the office example below is a runnable `Example_` using `absent` with no Go beyond existing wiring; A9 test shows unchanged output for existing documents. |
| **B** contradictions | none (A independent) | Medium. New hook inside `Add` and a new return field; the `Observed`-as-lookup-tick decision (D1); interplay with eviction. | Decided: separate hook, `MergePolicy` unchanged (it sees only same-ID pairs and widening it would break callers). Decided: lookup tick is `incoming.Observed` (D1); read-only closure; no shipped policy helper (D4). | Scenarios SB1-SB9 pass; the spaceport berth example resolves with a custom stronger-wins policy and again with a re-weighting policy; unused-path behaviour test passes; a spread delivery and an inferred fact are both subject to the policy (B9). |
| **C** relational claims | Stage 3 recipes (evidence); B optional (a policy can use roles) | High (design risk). The cheapest step (C1) may remove the need for C2; C2 touches identity-adjacent code. | Gate decided (D5): a real consumer's recorded need, then Stage 3 evidence. The design of C2 stays provisional until that need is written down. | Written evidence of the awkwardness first; then C1 passes SC1-SC2; C2-C4 only if the evidence says C1 is not enough, and then SC3-SC4 pass. |

Order if triggered: A, B, C. A and B are independent and can be built in either order or in parallel.

## 8. Testing / Key Scenarios

Levels follow `testing.md`: **unit** = table-driven package test; **example** = `Example_` test that doubles as documentation; **integration** = `declare` test loading a data file; **fuzz** = the existing `FuzzLoad`.

### Unit A (domain: office gossip)
Rule: `requires: [{type: hiring_freeze, subject: company}, {type: job_posting_seen, subject: company, absent: true}]`, `produces: {type: team_shrinking, subject: company}`, `confidence: 0.4`.

| # | Covers | Scenario | Level |
| --- | --- | --- | --- |
| SA1 | A1, A2, A4 | Given a holder with `hiring_freeze` and no `job_posting_seen`, when `Run`, then `team_shrinking` is derived and its premises list only the freeze fact. | example + unit |
| SA2 | A1, A2 | Given the same holder also holding `job_posting_seen` at confidence above its floor, then nothing is derived. | unit |
| SA3 | A2, A3 | Given `job_posting_seen` held at confidence below the requirement's floor, then the requirement counts as absent and the rule fires. | unit |
| SA4 | A5 | Given an unbound absent requirement and no unbound positive requirement, when `infer.New`, then `ErrInvalidRule`. | unit |
| SA5 | A6, A7 | Given two rules each requiring the absence of the other's product, then both fire in pass 1 (snapshot semantics); given `max_passes: 2` and a third rule requiring the absence of a pass-1 product, it does not fire in pass 2. Repeated runs give identical results. | unit |
| SA6 | A9 | Given a document with no `absent` key, then output equals a golden result captured before the change. | integration |
| SA7 | A8 | Given `"absent": "yes"` in a data file, then `Load` returns a decode error and does not panic. | unit + fuzz seed |

### Unit B (domain: spaceport berths)
Facts: type `berth`, subject a ship, `Discriminator` the claimed berth, so "bay-4" and "bay-7" are different facts. `conflicts(a, b)` = same type and subject, different ID.

| # | Covers | Scenario | Level |
| --- | --- | --- | --- |
| SB1 | B2, B3, B10 | Given a store with a `witnessed` berth claim (0.9) and an incoming `hearsay` claim for another berth (0.7), when `Add` with a custom "stronger rank, then confidence, wins" policy, then the incoming is `Rejected` and `Conflicted` is empty. | example + unit |
| SB2 | B4, B7 | Given a held `hearsay` claim (0.7) and an incoming `witnessed` claim (0.9) for another berth, then the held claim is removed, the incoming is `Inserted` and `Conflicted` lists the removed ID. | unit |
| SB3 | B3, B4, B7 | Given a custom policy that sets both claims to half confidence and keeps both, then both survive with the new confidences and `Conflicted` lists the held ID. | unit |
| SB4 | B5 | Given a policy returning `Remove` with an unknown ID, a NaN confidence, or the incoming ID in `Remove`, then `Add` returns `ErrInvalidResolution` and the store is unchanged. | unit |
| SB5 | B2 | Given the merge policy rejects the incoming (same ID, lower rank), then the conflict policy is not called. | unit |
| SB6 | B6 | Given `MaxFacts` reached and a resolution that removes one fact and inserts the incoming, then no eviction occurs; given no removal, the weakest is evicted as today. | unit |
| SB7 | B9 | Given a listener store with the policy and a spread delivery that conflicts, then `Transfer.Outcome` reflects the result and the policy ran with no change to `spread`. | integration |
| SB8 | B1 | Given no policy configured, then every existing knowledge test passes unmodified. | unit (existing suite) |
| SB9 | B8 | Given a policy and `go test -race`, then a single-goroutine run under an external lock is race-free (documents the contract). | unit |

### Unit C (domain: hospital ward handover), only if gated in
| # | Covers | Scenario | Level |
| --- | --- | --- | --- |
| SC1 | C1 | Given handover facts about patients with the receiving nurse inside `Payload`, when `Find` with `Where` testing that payload, then only that nurse's handovers return. | example + unit |
| SC2 | C1 | Given `Where` is nil, then `Find` behaves as before. | unit |
| SC3 | C2, C3 | (C2 only) Given facts with `Roles{from: a, to: b}`, then `Query.Roles{to: b}` returns them and two facts differing only in roles share one ID and merge by the normal policy. | unit |
| SC4 | C4 | (C2 only) Given `add_fact` with `roles` in a data file, then the stored fact carries them; a mistyped roles arg yields `ErrBadArgs`; renaming via `ArgNames.Roles` works and the default name is then unknown; a rename colliding with another arg is `ErrBadBindings`. | integration |

## 9. Cross-stage interfaces

(What Stage 5 relies on in types and hooks other stages also touch, for the lead to reconcile.)

1. **`knowledge.Store` and its `Add` pipeline.** Required order: validate, clamp, identity, merge decision, **conflict policy (new, Stage 5)**, write, `MaxFacts` eviction. If Stage 1's fading/maintenance step or Stage 4's indexing rewrites `Add`, keep this order. Stage 1 ships `Store.Fade(now)` (with `Fader` and `Fact.FadedAt`), which re-weights and forgets facts. `Fade` and the conflict hook are separate paths and neither invokes the other. `Fade` does not run the conflict policy (it adds nothing), and a conflict `Remove`/`Confidence` does not touch `FadedAt`. A conflict-policy confidence change leaves `FadedAt` as it was, so the next `Fade` decays from the same settle point (`max(Observed, FadedAt)`). Both `Fade` forgetting and conflict `Remove` are Store-internal deletions, and `Prune` and `MaxFacts` eviction are the other two; item 11 states the index obligation common to all four.
2. **New store option.** `knowledge.WithConflictPolicy(ConflictPolicy)`, alongside the existing `WithMergePolicy`. Types:
   ```go
   type ConflictPolicy func(incoming Fact, find func(Query) []Fact, rank func(source string) int) Resolution
   type Resolution struct {
       Reject     bool
       Remove     []string           // held fact IDs to delete
       Confidence map[string]float64 // new confidence per fact ID (held, or the incoming ID)
   }
   ```
   Any other stage adding a store option follows the same functional-option pattern; none may add a second hook that also mutates on `Add` without the lead resolving ordering.
3. **`knowledge.AddResult`** gains `Conflicted []string` (B7). Stage 5 relies on no other stage adding a field of the same name; `spread.Transfer` is **not** changed (it keeps only `Outcome`).
4. **`knowledge.Config`** is **unchanged** by Stage 5. If another stage adds fields, no collision.
5. **Fact identity / discriminator.** Unchanged. Contradictions rely on callers using `Discriminator` (or an explicit `ID`) to make rival claims distinct facts. If Stage 2 lets data files set the discriminator, that is what makes Unit B reachable from `add_fact` rules; Stage 2 adds `discriminator` and `discriminator_from` args to `add_fact`, named through `Bindings.ArgNames` (struct `ArgNames`, `DefaultArgNames()`, zero value valid). In Unit C the `roles` arg follows that same pattern: `ArgNames` gains one additive field `Roles` (tag `roles`, default `roles`, empty = default), which names the arg on both `add_fact` and `knows`. The arg is never hardcoded. The `Unknown(...)` allow-list follows Stage 2's union rule (R3.9) unchanged: the one unexported helper per name builds base args plus configured optional names, so `roles` is appended there, never replacing the list. A rename of `Roles` that collides with another arg of the same action is `ErrBadBindings` at registration, as Stage 2 specifies. `Roles` (C2) are explicitly **not** part of ID derivation, so the U+001F check and `per_event` logic are unaffected.
6. **`Fact` and `Query` (C2/C3 only).** Also `declare.Bindings.ArgNames` (see item 5). `Fact.Roles map[string]string` and `Query.Roles map[string]string`, `Query.Where func(Fact) bool`. The store's `clone` must deep-copy `Roles`.
7. **`Spreader.Transform` (Stage 1 widens it to see teller, listener and tick).** Stage 5 needs no change to it. Decision for Unit C only (D6): `Roles` behave like `Payload`, i.e. a Transform **may** change them (a rumour that swaps who did what is legitimate distortion), while `ID`, `Type`, `Subject`, `Discriminator`, `Source`, `Prov`, `Observed` and `Private` stay restored.
8. **Spreader filters.** `Encounter.Fact` is a `knowledge.Fact`, so filters see `Roles` automatically. No filter or `Encounter` change. Stage 2's typed filter helper needs nothing from Stage 5.
9. **`declare.Catalog`.** No change for Unit A (absence is data inside `inference:`) or Unit B (policy is Go). Rules already express absence as `not` over `knows`; Stage 2's attribute conditions are unaffected. Unit C (C4) adds an optional roles arg to `knows` and `add_fact` only, named by `Bindings.ArgNames.Roles` and added to each factory's allow-list via Stage 2's helper (R3.9), not by editing the list directly.
10. **`infer.Requirement`** gains `Absent bool`. If Stage 2 adds attribute matching to requirements, `absent` negates the whole match (type, subject, floor, sources and any attributes).
11. **Stage 4 indexing / benchmarks.** The conflict lookup uses `Find` semantics. An index must preserve `Find` results exactly (same ordering, same floor and expiry rules), so Stage 5's behaviour is index-independent. The index must also observe every Store-internal mutation, not only `Add`'s write: conflict `Remove` and `Confidence` (this PRD, B6), `Fade` confidence changes and forgetting (Stage 1, R12), `Prune`, and `MaxFacts` eviction. Conflict removals are described exactly like Fade's `Forgotten`: IDs deleted inside the store, reported to the caller (`AddResult.Conflicted` here, `FadeResult.Forgotten` there), and never routed through a public `Add`.

## 10. Decisions

Recorded by the lead on 2026-10-18. Where a decision changed the first draft, the change is stated.

- **D1.** The conflict lookup tick is `incoming.Observed`. A direct `Add` with `Observed: 0` looks up at tick 0, and the docs say so. (Confirms the draft.)
- **D2.** No retraction feature. Derived facts keep not tracking premises; `TTL` bounds staleness. (Confirms the draft.)
- **D3.** No data-file form of conflict policy; deferred until a consumer needs one. (Confirms the draft.)
- **D4.** `StrongerWins` is dropped. The library exports the hook and types only, and the Unit B example carries a custom policy. (Changed: the draft proposed keeping it as a Should. B10 now says so. Revisit only if the example genuinely reads worse.)
- **D5.** The Unit C gate is a real consumer's recorded need, not the spec author's judgement. (Confirms the draft.)
- **D6.** The Unit C roles arg for `add_fact` and `knows` is named through `Bindings.ArgNames.Roles`, an additive field on Stage 2's `ArgNames`, and the `Unknown(...)` allow-list follows Stage 2's union rule (R3.9). (Changed: the draft hardcoded the arg name `roles`; it is now configurable with default `roles`.)
