# PRD: Stage 3, documentation and patterns

Status: draft · Owner: lead agent (Stage 3 PRD by subagent) · Last updated: 2026-10-18

Conventions found: spec rules `doc/rules/specs.md` (purpose, API, config, behaviour, errors, concurrency, neutral examples); testing rules `doc/rules/testing.md` (stdlib only, table-driven, Examples in neutral domains, leak guard, `-race`, `go vet`); architecture rules `doc/rules/architecture.md`. No design system (library, no UI: section 7 is N/A). Test levels used here are the project's own: unit/table test, `Example_` test, repo-level guard test (`architecture_test.go`, `docs_test.go`, `leak_test.go`), and external-consumer check.

## 1. Problem and goal

**End user:** a developer integrating rumour_mill into a game or simulation (the "integrator").

Three things the current design already supports are invisible to them:

1. **Claims with several parties, group beliefs, and per-occurrence versus merged facts.** The facts model (type, subject, discriminator, payload) can express all three, but nothing says how. An integrator who cannot see it either abandons the library or invents an incompatible workaround, then asks for new API (Stage 5 "richer claims") that may not be needed.
2. **Data-file mistakes.** A misspelt condition name, argument key or layer only fails when the file is loaded (`declare` errors carry paths such as `rules[3].when.of[1]`). If loading happens only at game start, a typo ships. A convention (test that every data file loads) turns this into a failing CI run, but it is undocumented.
3. **No evidence the library works outside its own repo.** All four worked domains live in this module's tests. Architecture rule 11 and the roadmap hold v0.x until two real consumers use it. Without a recorded second-consumer trial, every API-shape decision (Stages 1, 2, 5) is guesswork.

**Goal:** an integrator can, from the docs alone, (a) model the three patterns correctly, (b) add a load-everything test to their own repo, and (c) once a second consumer exists, see what it needed, so the v0.1.0 tag rests on evidence. (a) and (b) do not depend on (c).

**Success measures (checkable):**
- Each of the three recipes has a runnable `Example_` that passes, and the doc text cannot drift from it (R6, R7).
- `declare` has a documented hygiene recipe whose pattern is already demonstrated by an in-repo test (existing `declare/document_test.go` loads every `testdata/*.json`); the doc points at it.
- Once a consumer exists (gated task, section 7 U3), a findings record from a real out-of-repo consumer exists, listing each place the consumer needed something the library lacked or got wrong, each tagged as docs / bug / API request.

**Why not just add API now:** a recipe that shows the current model suffices removes pressure for new API; one that exposes real friction is the evidence Stage 5 is gated on. Both outcomes help the integrator.

## 2. Scope

### In scope
- Recipes document `doc/recipes.md` (decision D1) with three recipes plus the hygiene convention, written against the final Stage 1 and Stage 2 API.
- One runnable `Example_` per recipe, in a neutral domain.
- A guard test that keeps the doc and examples in sync.
- Second-consumer trial as a **gated task** (starts only when a consumer exists, decision D3; nothing else in this PRD waits on it): a findings record, plus triage of its findings into docs fixes (done here) and API requests (deferred to roadmap stages).
- Index and roadmap updates, performed by the lead, not by this PRD's author.

### Out of scope
- Any new exported API, config field, or data-file feature. If a recipe is awkward, record that as a finding for Stage 5; do not fix it here.
- A shipped "load-all-files" helper in `declare` (smallest surface; the pattern is a few lines in the integrator's own test).
- Group membership, audience selection, event delivery (stay the caller's; overview.md "Not planned").
- Choosing the second consumer's design or building its features beyond what the library needs.
- The v0.1.0 tag itself (this stage gates it, does not perform it).

### Priority
- **Must:** R1-R4 (recipes), R5 (hygiene), R6-R7 (examples and guard).
- **Should, gated:** R8-R10 (second-consumer trial and findings); start when a consumer exists, and do not block U1/U2 or this PRD's acceptance on them.
- **Could:** R11 (findings triage into doc fixes) if findings arrive in time.
- Under time pressure, cut R11 first, then trim recipe prose; never cut the runnable examples, because an unverified recipe is worse than none for the integrator.

## 3. Requirements

Each requirement is one behaviour, traceable to the integrator's goal.

**Recipes**
- **R1. Multi-party claims recipe.** Shows how to record a claim involving more than one party ("A wronged B") using only existing fields (no new API): `Subject` is the party the claim is *about*, other parties go in `Discriminator` (identity) and/or `Payload` (data), and the recipe states what is queryable (`Query` matches `Type` and `Subject` only) and what is not (discriminator and payload are not queryable). It shows the "one fact per party" alternative for when each party must be findable by `Find`, and states the cost (several facts, they merge independently).
- **R2. Group beliefs recipe.** Shows a group as just another `spread.Holder` with its own `knowledge.Store`, with the two flows: member to group (a `spread.Typed[H]` filter, written against the caller's own holder type with no hand assertion, decides when a member reports; the recipe states that `Typed` fails closed when a teller or listener is not an `H`, so a group and its members must share one holder type or the filter be composed with `logic.Any`) and group to member (the group "tells" listeners via `Spread`). States that membership is the caller's, and that the group store has its own ranks and `MaxFacts`.
- **R3. Per-occurrence versus merged recipe.** Shows the same claim stored merged (default identity: repeats merge under the merge policy, confidence updates) versus per-occurrence (`Discriminator` or explicit `ID` set to an event ID; each occurrence is a distinct fact), including the `per_event` option of the built-in `add_fact` action and its data-file variants: `discriminator` (a constant) and `discriminator_from` (a name registered in `declare.Bindings.Discriminators`, such as an event or victim source), which are mutually exclusive with `per_event` and with each other, and an unknown source name fails at load with `ErrBadArgs`. The data-file variant uses the default arg names (`DefaultArgNames`); the recipe notes they are renameable through `Bindings.ArgNames`. States that an empty result from a discriminator source falls back to the default merged identity. States consequences: merged facts cannot count occurrences; per-occurrence facts consume `MaxFacts` and are evicted weakest-first.
- **R4. Recipe format.** Each recipe has: the problem in one sentence, the pattern, a neutral-domain example (domain not used by another recipe), a "limits" paragraph naming what the pattern cannot do, and a pointer to the roadmap stage that would address the limit if it proves real.

**Hygiene**
- **R5. Data-file hygiene convention.** Documented in `declare` spec and in the recipes doc: every data file the game ships must be loaded in a test using the same catalog registrations as production (including `RegisterKnowledgeOps` and, if the files use `attr_equals`, `attr_in` or `attr_same`, `RegisterAttributeOps` with the same `AttributeNames` and `Attributes` as production, plus every discriminator source in `Bindings.Discriminators` the files name), exercising `declare.Load`, `LoadRules`, filter resolution (`ConditionAll` for the spreading filters) and `infer.New`. States why (name errors only surface at load) and what it does not catch (a rule that loads but never fires; for that, write a behaviour test). Points to the in-repo test as the model (`declare/document_test.go`) and shows the shape in a few lines.

**Verification**
- **R6. One Example per recipe.** `Example_recipe<Name>` tests with `// Output:` blocks, covering R1, R2, R3 and R5 (for R5, an example that loads an embedded document and also shows a deliberately broken name producing a path-bearing error). They use neutral domains, only exported API, and run under `-race` and `go vet`.
- **R7. Doc/example sync guard.** A guard test fails if (a) a recipe heading in the recipes doc names an `Example_recipe*` that does not exist, or (b) an `Example_recipe*` exists that no recipe references, or (c) the recipes doc is not linked from `doc/README.md` and the root `README.md`. The leak guard (`TestNoDomainVocabulary`) covers the new docs without change.

**Second consumer**
- **R8. Trial.** A project outside this repository integrates the library via a normal `go get` of a version or commit (not a `replace` to this checkout, except as a temporary development aid). The project must use at least `knowledge` and `spread`, and one of `infer` or `declare`.
- **R9. Findings record.** `doc/design/second-consumer.md` records, per finding: what the consumer tried, what happened, category (docs gap, bug, API friction, missing capability), severity (blocked, workaround needed, cosmetic), and the proposed destination (roadmap stage, or "fixed here"). It names the consumer only by a neutral label; no private project details.
- **R10. Gate definition.** The record ends with an explicit verdict against the v0.1.0 gate: no unresolved "blocked" findings, every "bug" fixed, every "API friction" either fixed or filed against a named roadmap stage.
- **R11. Docs findings fixed.** Findings categorised "docs gap" are fixed in this stage (README, specs or recipes), each with a changelog line.

## 4. Non-functional requirements

- **Correctness of docs:** recipe code is copied from the passing Examples, not written separately (R6, R7).
- **Neutrality:** examples and findings contain no domain vocabulary from the originating game; enforced by the existing leak guard. The second consumer's own domain terms must not appear in this repository's docs beyond a neutral label.
- **Altitude:** recipes describe patterns over existing API; they do not prescribe the integrator's architecture.
- **Observability:** N/A (library; no runtime).
- **Rollout and compatibility:** documentation and tests only; no API change, so no migration or rollback. If Stage 1 or Stage 2 change an API that a recipe uses, the recipe's Example fails to compile and is updated in the same change (this is the desired coupling).
- **Concurrency/determinism:** examples are deterministic (fixed ticks, no RNG).

## 5. UX and design

N/A. The library has no user interface. The integrator experience is the documentation structure: the README links to the recipes doc; recipes are short and independent so they can be read alone.

## 6. Stakeholders and acceptance criteria

| Role | Who | Sign-off on |
| --- | --- | --- |
| Owner / approver | the user (project owner) | whole PRD, recipe wording |
| Reconciler | lead agent | cross-stage consistency, index/roadmap updates |
| Second-consumer owner | none yet (decision D3: gated) | R8-R10 |

| Req | Acceptance criterion |
| --- | --- |
| R1-R3 | The recipe exists, has all R4 parts, its claims about queryability/merging match the knowledge spec, and its Example passes. |
| R4 | A reviewer finds problem, pattern, example, limits and roadmap pointer in each recipe. |
| R5 | The convention appears in `declare` spec and recipes doc; the example shows a path-bearing load error. |
| R6 | `go test -race ./...` and `go vet ./...` pass; examples have `// Output:` blocks. |
| R7 | The guard fails when a heading is renamed without its Example (demonstrated once in review) and passes otherwise. |
| R8-R9 | Findings record exists with at least the fields in R9 per finding, and a line stating which packages the consumer used. |
| R10 | Verdict section present; every "API friction" has a destination. |
| R11 | Each docs-gap finding links to its fix. |

## 7. Phases and units

| Unit | Contents | Depends on | Risk | Decided / assumption |
| --- | --- | --- | --- | --- |
| U1 Recipes + Examples + guard | R1-R4, R6, R7 | **Stage 1 and Stage 2 landed** (decision D4), so recipes are written once against `spread.Typed`, `RegisterAttributeOps`, `discriminator_from` | Low. Recipes are over settled API | Decisions D1 (doc placement), D2 (test location), D4 (ordering) |
| U2 Hygiene | R5 | U1 (example), existing `declare` tests | Low | Decision: document, do not add API |
| U3 Second consumer | R8-R11 | U1 (so the consumer starts from the recipes and the gap is measurable); a tagged or committable library state; **a consumer being nominated** | **High**: depends on a project and person outside this repo; findings may force changes in other stages | Decision D3: gated task, not scheduled, no owner yet; unit is done only when a consumer exists and R10 is recorded |

Order: Stages 1 and 2, then U1 and U2 in parallel; U3 starts whenever a consumer exists and never blocks U1/U2 acceptance. U3's findings feed Stage 5 and may reopen Stage 2 decisions.

## 8. Testing and key scenarios

| # | Scenario (unit) | Given / When / Then | Level |
| --- | --- | --- | --- |
| S1 (U1, R1) | Multi-party claim, subject query | Given a claim of type `owes_favour`, subject `ana`, discriminator `ben`; When `Find({Type:"owes_favour", Subject:"ana"})`; Then it is returned, and a query for subject `ben` returns nothing | `Example_` |
| S2 (U1, R1) | One-fact-per-party alternative | Given the same claim stored as two facts (one per party); When each party's subject is queried; Then each finds its own fact | `Example_` |
| S3 (U1, R2) | Group as holder with typed filter | Given a member and a group store of one holder type and a `spread.Typed` filter allowing only members to report; When the member `Spread`s to the group, a non-member is refused, and the group `Spread`s to a second member; Then the second member holds the fact with two hops of decay and `Hops==2` | `Example_` |
| S4 (U1, R3) | Merged vs per-occurrence | Given the same claim added twice, once without and once with distinct discriminators; Then, with equal source and equal confidence, the first yields one fact (second add `Rejected`, ties keep existing) and the second yields two facts | `Example_` |
| S5 (U1, R3) | Eviction edge case | Given `MaxFacts` of 2 and three per-occurrence facts of different confidence; Then the weakest is evicted and the example shows which | `Example_` |
| S6 (U2, R5) | Hygiene catches a typo | Given an embedded document with a misspelt condition name; When the load-everything test runs; Then it fails with a path-bearing `ErrUnknownName` error | `Example_` |
| S7 (U2, R5) | Hygiene limit | Given a rule that loads but whose condition is never true; Then loading succeeds, and the doc states a behaviour test is needed | doc review (no test) |
| S8 (U1, R7) | Guard: orphan example | Given an `Example_recipeX` no recipe references; Then the guard fails | repo guard test |
| S9 (U1, R7) | Guard: missing example | Given a recipe heading naming a nonexistent example; Then the guard fails | repo guard test |
| S10 (U1, R7) | Guard: unlinked doc | Given the recipes doc not linked from the README; Then the guard fails | repo guard test |
| S11 (U3, R8) | Consumer compiles from a released ref | Given the consumer's `go.mod` requires the library by version or commit; When it builds and its tests run; Then they pass without `replace` | external-consumer check |
| S12 (U3, R10) | Verdict recorded | Given findings exist; Then `second-consumer.md` has a verdict against R10 | doc review |

| S13 (U1, R3) | Data-file discriminator | Given `add_fact` with `discriminator_from: "victim"` and a registered `victim` source; When two events with different victims run; Then two facts; same victim, one merged fact; unknown source name fails at load | `Example_` |
| S14 (U2, R5) | Hygiene covers attribute ops | Given a document using `attr_equals` and a catalog that omitted `RegisterAttributeOps`; When the load-everything test runs; Then it fails with `ErrUnknownName` and a path | `Example_` |

Lowest-level rule: S1-S6 are Examples because the doc must show them working; S8-S10 are guard tests because they are repo-wide invariants.

## 9. Decisions

Recorded from the lead's answers (2026-10-18):
- **D1.** Recipes live in `doc/recipes.md`, linked from the README and `doc/README.md`, not inline in the README.
- **D2.** Recipe examples live in a root-level external-package test file (`recipes_test.go`, package `rumourmill_test`); root tests already exist, so library import boundaries are unchanged.
- **D3.** No second consumer can be nominated now. U3 (R8-R11) is an explicit gated task: it starts when a consumer exists, records its findings then, and nothing else in this PRD depends on it. The v0.1.0 tag stays blocked on U3's verdict (R10) but not on U1/U2.
- **D4.** U1 recipes land after Stages 1 and 2, so they are written once against the final API.
- **D5.** No shipped load-all helper in `declare` (R5 documents the pattern instead).

Remaining assumptions pending validation:
- **A1.** Stage 2 names are as in `doc/specs/prd-stage2-less-glue-code.md` at the time of writing; if they change, recipe examples fail to compile and are updated.

## Cross-stage interfaces

Final Stage 1 and Stage 2 API as stated by the lead and `prd-stage2-less-glue-code.md`. Stage 3 touches no type itself; it documents and exercises them, so each item is a compile-time dependency of an Example.

- **`knowledge.Fact` identity.** ID derived from Type, Subject, Discriminator when empty; `Discriminator` and explicit `ID` control merge vs separate. Unchanged by Stages 1 and 2.
- **`knowledge.Query`.** Matches Type and Subject only (discriminator and payload not queryable; Stage 2 excludes a discriminator query from `knows`). If a later stage changes this, recipe R1 shrinks.
- **`knowledge.Store` and `Config`.** Assumes `MaxFacts`, eviction order, `SourceRanks` (S5). Stage 1 adds `Store.Fade`, `WithFader`, `Fact.FadedAt`, `Config.Fade`; recipes do not use them, but if a fader changes confidence the `MaxFacts` eviction order in S5 is unaffected only while no fader is installed (examples install none).
- **`spread.Spreader.Transform`.** Stage 1 signature `func(e Encounter, delivered knowledge.Fact) knowledge.Fact`. Stage 3 recipes do not use `Transform` or `TypedTransform`.
- **Typed filters.** R2 uses `spread.Typed[H Holder](func(TypedEncounter[H]) bool) logic.Condition[Encounter]` with `spread.WithFilter`. Relies on Stage 2's fail-closed rule for a holder that is not an `H` (R1.2-R1.5 there).
- **`declare.Catalog`.** Uses `RegisterCondition`, `RegisterAction`, `RegisterKnowledgeOps`, `ConditionAll`, `LoadRules`, `Load`, `ErrUnknownName`, `ErrBadArgs`, and `(*Catalog[C]).RegisterAttributeOps(Attributes[C], AttributeNames) error` (default names `attr_equals`, `attr_in`, `attr_same` via `DefaultAttributeNames`, zero value valid). R5 treats `RegisterAttributeOps` as part of the production registrations.
- **`declare.Bindings`.** Fields `Store, Now, Subject, EventID` plus Stage 2's `Discriminators map[string]func(C) string` and `ArgNames ArgNames` (`DefaultArgNames`). R3 and S13 use `discriminator_from`; `discriminator`, `discriminator_from` and `per_event` are mutually exclusive (Stage 2 R3.5).
- **Ordering.** U1/U2 after Stages 1 and 2 (D4). U3 gated on a consumer (D3) and independent of the rest.
- **Docs-and-index files.** Stage 3 adds `doc/recipes.md`, `doc/design/second-consumer.md` (when U3 starts), `recipes_test.go` and edits `README.md`, `doc/README.md`, `doc/specs/declarative.md`, `CHANGELOG.md`. Stages 1 and 2 also edit the README and the declarative spec; the lead sequences those edits.
