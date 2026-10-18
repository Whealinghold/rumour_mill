# Plan: Stage 3, documentation and patterns

**Status: Proposed.** Sequences [PRD Stage 3](../specs/prd-stage3-docs-and-patterns.md) (R1-R11, units U1-U3, decisions D1-D5) into phases, against the [roadmap](../roadmap.md), `doc/rules/architecture.md` (prime directive, "Idiomatic Go": usage first, smallest exported surface), `doc/rules/testing.md` (stdlib only, Examples in neutral domains, `-race`, `go vet`, leak guard) and `doc/rules/specs.md` (specs updated in the same change as the code). A plan decides nothing: where the PRD is silent, the call is listed under Decisions or Assumptions below.

This stage adds **no exported API**. Its deliverables are one markdown doc, one external-package test file, one guard test, and spec/index edits.

## Scope

**In scope**
- `doc/recipes.md` with recipes R1 (multi-party claims), R2 (group beliefs), R3 (per-occurrence versus merged) and the R5 hygiene section, each in the R4 format.
- `recipes_test.go` (package `rumourmill_test`, PRD D2): one `Example_recipe*` per recipe (S1-S6, S13, S14).
- A doc/example sync guard (R7, S8-S10).
- Hygiene convention written into `doc/specs/declarative.md` (R5).
- Gated: second-consumer trial and findings record (R8-R10), docs-findings fixes (R11).

**Out of scope** (PRD section 2)
- Any new exported API, config field or data-file feature. Awkwardness found while writing a recipe is recorded for Stage 5 (PRD U3 findings or a note in the recipe's "limits" paragraph), never fixed here.
- A `declare` load-all helper (D5).
- Group membership, audience selection, event delivery (roadmap "Not planned").
- The v0.1.0 tag itself (this stage supplies the verdict; it does not tag).
- Stages 4 and 5 work. Stage 5's gate 2 consumes this stage's recipes as evidence; nothing here schedules it.

## Entry conditions (hard gate on all of Phases 1-5)

Stages 1 and 2 have landed (PRD D4). Before Phase 1, confirm by `go build ./... && go vet ./...` plus reading the code that these exist with the stated shapes; if any is missing or renamed, stop and update this plan and the PRD's assumption A1 rather than coding against a guess:
`spread.Typed[H]`, `spread.TypedEncounter[H]`, `spread.WithFilter`, `declare.Bindings.Discriminators`, `declare.Bindings.ArgNames`, `declare.DefaultArgNames`, `(*Catalog[C]).RegisterAttributeOps`, `declare.Attributes[C]`, `declare.AttributeNames`, `declare.DefaultAttributeNames`, the `add_fact` args `discriminator` / `discriminator_from` / `per_event`, `knowledge.Fact.Prov.Hops` (already present) and `knowledge.AddResult.Outcome` / `Evicted` (already present). (Today's tree has `declare.Bindings`, `RegisterKnowledgeOps`, `ConditionAll`, `LoadRules`, `Load`, `ErrUnknownName`, `ErrBadArgs`, `spread.WithFilter`, `knowledge.Store.Add/Find`, `infer.New`; the rest is Stage 2.)

## Decisions (made by this plan; PRD decisions are honoured, not repeated)

- **G1. Examples before prose.** Phases write the `Example_` first and the recipe text after, quoting it. Rationale: architecture "Idiomatic Go" rule 1 (usage first); an unverified recipe is worse than none (PRD priority).
- **G2. Guard logic is a pure function.** `checkRecipes(doc string, readmes map[string]string, examples []string) []string` in `recipes_guard_test.go` (package `rumourmill`, beside `docs_test.go`), returning problems; table tests feed it broken inputs (S8-S10), one real-repo test feeds it the files. Rationale: S8-S10 are testable without mutating the repo, and R7's "demonstrated once in review" becomes a permanent test.
- **G3. Linking convention the guard parses.** Each recipe is a `## Recipe: <title>` heading whose first line below it is `Examples: ` followed by one or more backticked `Example_recipe*` names. Rule (a): every name referenced exists in `recipes_test.go` (found with `go/parser`, top-level funcs matching `Example_recipe*`). Rule (b): every such func in `recipes_test.go` is referenced. Rule (c): `doc/README.md` and root `README.md` each contain the link text `recipes.md`. Rationale: matches R7 literally; a plain-text convention needs no new tooling. A recipe may name several Examples (R3 has three).
- **G4. Doc code cannot drift.** Fenced blocks in `recipes.md` tagged ```` ```go example=Example_recipeX ```` must consist only of lines that appear (trimmed) as lines of that Example function body, located with `go/parser` node positions over the file text. Rationale: the PRD NFR says recipe code is copied from passing Examples; this makes that checkable for about 20 lines. Cheap to drop (delete one check) if it proves noisy; flagged under Assumptions.
- **G5. One shared fixture, no helper package.** Recipe Examples define small unexported types in `recipes_test.go` (a holder with an id and a `*knowledge.Store`, a fixed-tick `now`). No exported test helper and no `internal/` package: the file is the only user. Rationale: smallest surface.
- **G6. Domains per recipe (neutral, distinct, PRD R4).** R1 neighbours trading favours; R2 a book club and its members; R3 a village notice-board; R5 hygiene a lending library. Each is checked against `leak_test.go`'s banned list (the guard scans `doc/recipes.md`; `_test.go` files are not scanned but follow the same rule, testing.md rule 3). Rationale: rule 3 plus R4's "domain not used by another recipe".
- **G7. Hygiene example lives in `recipes_test.go`, not `declare`.** `Example_recipeHygiene` and `Example_recipeHygieneAttributes` load an inline JSON string constant (no `testdata` dir at root, no new files outside the PRD's list). The in-repo model it points at stays `declare/document_test.go` (loops every `testdata/*.json`, line ~195). Rationale: the example must be next to the guard that ties it to the doc (G3); `declare` tests are untouched.
- **G8. Roadmap wording.** The roadmap says "README sections" for recipes; PRD D1 says `doc/recipes.md` linked from the README. The PRD wins; the lead edits the roadmap line (see "Spec and index changes").
- **G9. Findings that exceed docs.** In Phase 7, anything categorised bug is fixed in its own package with its own spec update under normal spec-first rules; API friction is only filed (against Stage 5 or another named stage), never built here. Rationale: PRD out-of-scope rule on new API.

## Assumptions this plan makes (flagged for override)

- **A1 (from PRD).** Stage 1 and 2 names are as in the PRDs at the time of writing; a rename breaks compilation of the Examples and is fixed in the same change.
- **A2.** Examples register no `Fade`/`WithFader` (PRD cross-stage note), so S5's eviction order is the plain weakest-first order.
- **A3.** S3's "two hops of decay" figure is whatever the default `spread.Config` decay yields; the Example prints the computed confidence and `Hops`, and the doc quotes the printed output rather than a hand-computed number.
- **A4.** G4 (code-block provenance check) ships in Phase 5; if reviewers find it fragile, it is removed without affecting R7.
- **A5.** The Phase 1-5 changes go in as one change or a short series; the lead sequences README/declarative-spec edits with Stages 1 and 2 (PRD "Docs-and-index files").

## Phase 1 - Entry check, multi-party and group Examples (U1: R1, R2, R6 part)

**Why first:** Examples are the source of truth for the prose (G1) and fail to compile if Stage 1/2 drifted, which is the earliest possible signal.
- Run the entry conditions above.
- Create `recipes_test.go` (package `rumourmill_test`, PRD D2). `architecture_test.go` checks only non-test files, so importing every package here does not breach import boundaries.
- `Example_recipeMultiParty` (S1): `knowledge.Store.Add` a fact `Type:"owes_favour", Subject:"ana", Discriminator:"ben"`; `Find` by `Subject:"ana"` returns it, `Find` by `"ben"` returns nothing; payload shown not queryable.
- `Example_recipeMultiPartyPerParty` (S2): the same claim as two facts, one per party; each subject finds its own.
- `Example_recipeGroup` (S3): one holder type (reuses existing `spread.Holder` shape: see how `declare/helpers_test.go` or `spread` tests build holders and copy the pattern, do not invent a new interface); `spread.Typed[H]` filter lets only members report to the group; member to group, a refused non-member, group to second member; print confidence and `Prov.Hops==2`. Include the fail-closed note as a comment tied to R1.3 in Stage 2.
- Each Example uses fixed ticks, no RNG, only exported API.

**Tests:** these are the `Example_` level (the project's own level for "the doc must show it working", PRD section 8): S1-S3 with `// Output:`. Run `go test -race ./...`, `go vet ./...`.

**Observability & rollback:** N/A, tests only. Rollback is deleting the test file.

**Acceptance:** `go test -race -run 'Example_recipe' .` passes; each Example has an `// Output:` block; the leak guard passes.

## Phase 2 - Per-occurrence versus merged Examples (U1: R3)

**Why second:** independent of Phase 1 but shares the file and fixtures; keeping it separate keeps each change reviewable.
- `Example_recipeOccurrence` (S4): same claim added twice, merged (second `Add` reports the rejected outcome on equal source and confidence, ties keep existing: assert via printed `AddResult`/outcome string) versus with distinct `Discriminator` values (two facts).
- `Example_recipeOccurrenceEviction` (S5): `knowledge.Config.MaxFacts = 2` (start from `knowledge.DefaultConfig()`), three per-occurrence facts of different confidence; print which was evicted.
- `Example_recipeOccurrenceData` (S13): a small document using `add_fact` with `discriminator_from: "victim"` and a registered `Bindings.Discriminators["victim"]` source, wired through `declare.Catalog`, `RegisterKnowledgeOps`, `LoadRules` and a `rules.Registry`; two events with different victims give two facts, the same victim one merged fact; a second snippet shows an unknown source name failing with `ErrBadArgs`; one line each for `discriminator` (constant) and mutual exclusion with `per_event` (load error). Empty-source fallback (R3.6) shown by a source returning `""`.

**Tests:** `Example_` level (S4, S5, S13). Table-driven unit tests for the same semantics already exist in `knowledge` and `declare`; these Examples do not re-test them, they document them.

**Observability & rollback:** N/A. Rollback is deleting the added functions.

**Acceptance:** all three pass under `-race`; printed output shows the stated facts and the evicted one.

## Phase 3 - Hygiene Examples and the `declare` spec section (U2: R5)

**Why third:** needs the catalog wiring pattern from Phase 2 and Stage 2's `RegisterAttributeOps`.
- `Example_recipeHygiene` (S6): a `loadEverything(doc string) error` helper of a few lines: for an embedded document run `declare.Load`, `LoadRules`, `ConditionAll` on the spreading filter nodes, `infer.New`; first with a good document (prints "ok"), then with a misspelt condition name printing the path-bearing `ErrUnknownName` error (for example `rules[0].when...`; the exact path text is whatever `declare` produces, printed, not asserted by hand).
- `Example_recipeHygieneAttributes` (S14): a document using `attr_equals` against a catalog that omitted `RegisterAttributeOps`; prints the `ErrUnknownName` with a path; then the same with the registrations in place. Shows `AttributeNames` and `Attributes` must match production.
- Edit `doc/specs/declarative.md`: add the data-file hygiene convention under "5. Testing" (or a new short subsection), covering: load every shipped file in a test with production catalog registrations (`RegisterKnowledgeOps`, `RegisterAttributeOps` with the same `AttributeNames` and `Attributes`, every `Bindings.Discriminators` source named), exercise `Load`, `LoadRules`, `ConditionAll`, `infer.New`; why (name errors only surface at load); what it does not catch (a rule that loads but never fires: write a behaviour test; S7 is doc-review only); pointer to `declare/document_test.go` as the in-repo model and to the recipe. No other change to the spec (no API change).

**Tests:** `Example_` level (S6, S14). S7 has no test by PRD design; it is a doc-review item checked at Phase 4 acceptance.

**Observability & rollback:** N/A. Spec edit reverts with the commit.

**Acceptance:** both Examples pass; `declarative.md` contains the convention, the "does not catch" sentence, and the pointer; per `specs.md` rule 5 the spec change is in the same change as the Examples.

## Phase 4 - `doc/recipes.md`, links, changelog (U1/U2 prose: R1-R5, R7c)

**Why fourth:** prose quotes Phase 1-3 output (G1, A3).
- Write `doc/recipes.md`: short intro (integrator audience, recipes independent), then `## Recipe: ...` sections per G3 for multi-party claims, group beliefs, per-occurrence versus merged, and a hygiene section (`Examples:` line naming the hygiene Examples). Each R1-R3 recipe has the R4 parts: one-sentence problem, pattern, quoted example (tagged code blocks per G4), "limits" paragraph, and a pointer to the roadmap stage that would address the limit (Stage 5 "richer claims" for multi-party; no stage for groups beyond "caller's"; none for occurrence counting, state the limit plainly).
- Content obligations, each traced to a requirement: R1 states `Query` matches `Type` and `Subject` only and the cost of one-fact-per-party; R2 states `Typed` fails closed, one holder type or `logic.Any`, membership is the caller's, group store has its own ranks and `MaxFacts`; R3 states the empty-source fallback, renameable arg names via `Bindings.ArgNames`, merged facts cannot count occurrences, per-occurrence facts consume `MaxFacts` and evict weakest-first.
- Cross-check each queryability and merge claim against `doc/specs/knowledge.md` (read-only; fix the recipe, not the spec, if they disagree; if the spec is wrong, stop and raise it).
- Link from root `README.md` (a "Recipes" line near "Declarative loading") and add the `recipes.md` index entry to `doc/README.md` (implementer edit, needed for Phase 5 guard rule (c)); add a `CHANGELOG.md` line under unreleased "Added".

**Tests:** doc review plus the Phase 5 guard (not yet written here); `go test ./...` still passes. Leak guard (`TestNoDomainVocabulary`) scans the new doc automatically.

**Observability & rollback:** N/A; revert the doc commit.

**Acceptance:** a reviewer finds problem, pattern, example, limits and roadmap pointer in each recipe (R4); S7's limit is stated; leak guard passes.

## Phase 5 - Doc/example sync guard (U1: R7)

**Why fifth:** it validates Phase 4's doc and Phases 1-3's Examples together; writing it after avoids a red build in the middle.
- Add `recipes_guard_test.go` (package `rumourmill`) with `checkRecipes` (G2) implementing G3 rules (a)-(c) and, per G4, code-block provenance.
- Table test `TestCheckRecipes` (S8, S9, S10, plus a passing case): orphan Example, missing Example, unlinked doc, a code line not present in its Example.
- `TestRecipesInSync` reads `doc/recipes.md`, `README.md`, `doc/README.md` and parses `recipes_test.go` with `go/parser` (stdlib; no new dependency).

**Tests:** table-driven (testing.md rule 2), hermetic: only reads repo files. Pyramid: this is the repo-guard level, same as `architecture_test.go`/`docs_test.go`.

**Observability & rollback:** N/A. If the guard proves wrong, remove the single file; nothing depends on it.

**Acceptance:** guard passes on the real repo; renaming a recipe's `Examples:` entry makes `TestRecipesInSync` fail (done once by hand, noted in the change description); `go test -race ./... && go vet ./...` clean.

## Phase 6 - Closing the in-repo part of the stage (U1/U2 done)

**Why:** gives a clear "Stage 3 core complete" point that does not wait on U3 (PRD D3).
- Run the full suite with `-race` and `go vet`; `just test-race` and `just lint`.
- Lead-owned edits (not done by the implementing agent, per PRD): `doc/README.md` entry for this plan (the `recipes.md` entry is Phase 4), roadmap Stage 3 wording (G8) and status marker for "Recipes" and "Data-file hygiene"; the second-consumer bullet stays open.

**Tests:** the whole suite; no new tests.
**Observability & rollback:** N/A.
**Acceptance:** PRD acceptance rows R1-R7 satisfied; PRD table for U1/U2 is complete; v0.1.0 remains blocked only on Phase 7.

## Phase 7 - GATED: second-consumer trial (U3: R8-R10)

**Gate (not scheduled work):** starts only when (i) a consumer outside this repository is nominated by the owner, and (ii) the library is at a tagged or committable state that the consumer can `go get`. No owner exists today (PRD D3). Nothing in Phases 1-6 waits on this phase, and this plan sets no date.
- The consumer integrates via a normal `go get` of a version or commit (no `replace` except as a temporary aid), using `knowledge`, `spread` and one of `infer`/`declare` (R8, S11 is checked in the consumer's own repo).
- Create `doc/design/second-consumer.md`: per finding, what was tried, what happened, category (docs gap, bug, API friction, missing capability), severity (blocked, workaround needed, cosmetic), proposed destination (roadmap stage or "fixed here"); the consumer named by a neutral label only; a line listing packages used (R9).
- End with an explicit verdict against the v0.1.0 gate: no unresolved "blocked", every "bug" fixed, every "API friction" fixed or filed against a named stage (R10; S12 is doc review).

**Tests:** the consumer's build and tests are the external-consumer check (S11); nothing is added to this repo's suite. The existing leak guard scans the findings doc, so no consumer domain terms may appear.

**Observability & rollback:** N/A. The findings doc is additive; a fix to a bug found follows normal spec-first practice in its own change (G9).

**Acceptance:** record exists with R9 fields per finding and R10 verdict; every API friction has a destination.

## Phase 8 - GATED, Could: findings triage (U3: R11)

**Gate:** Phase 7 has produced findings; cut first under time pressure (PRD priority).
- Each "docs gap" finding is fixed in `README.md`, the relevant spec, or `doc/recipes.md`, with a `CHANGELOG.md` line, and linked from its row in `second-consumer.md`. Recipe edits keep the Phase 5 guard green.
- Findings that need API are not built; they are linked to Stage 5 gate 2 evidence (or the named stage), where the gate's own conditions apply.

**Tests:** Phase 5 guard for recipe edits; otherwise doc review.
**Observability & rollback:** N/A.
**Acceptance:** every docs-gap finding links to its fix (R11).

## Spec and index changes in the same change as the code

| File | Change | Owner | Phase |
| --- | --- | --- | --- |
| `doc/specs/declarative.md` | data-file hygiene convention (R5) | implementer | 3 |
| `doc/recipes.md` | new | implementer | 4 |
| `README.md` | link to recipes | implementer | 4 |
| `CHANGELOG.md` | unreleased "Added" lines | implementer | 4 (and 8) |
| `doc/README.md` | index entry for `recipes.md` | implementer | 4 |
| `doc/README.md` | index entry for the plan; `second-consumer.md` when it exists | lead | 6 (7) |
| `doc/roadmap.md` | Stage 3 wording/status (G8) | lead | 6 |
| `doc/design/second-consumer.md` | new, gated | implementer | 7 |

Not changed, deliberately: `knowledge.md`, `spreading-and-inference.md`, `config.md`, `rules.md`, `overview.md`. The recipes only describe behaviour those specs already state (Stage 2 owns the `Typed` and attribute specs); a discrepancy found while writing is a stop-and-raise, not a silent edit.

## Security review

No new input, endpoint, permission, secret or trust boundary: the guard and Examples read repo files and run in-process. The one input path touched, `declare.Load` of a data file, is exercised with embedded constant strings, so no untrusted data. Worst case from the guard: reading `doc/recipes.md` with a malformed heading, which yields a failing test, not a panic (return errors, architecture rule 12). The hygiene recipe tells integrators to load their own shipped data files in their own tests; it should note that files from outside the integrator's control remain subject to the `declare` limits already in the spec. Phase 7 handles a third party's code only through `go get` in their repo; no consumer-private details enter this repository.

## Compatibility & rollout

No API, schema or config contract changes, so no expand-contract is needed. Coupling is intentional (PRD NFR): if a later change breaks an API an Example uses, the Example fails to compile and is updated in that same change; the guard fails if a recipe and its Example separate. Phases 1-6 can ship as one change or sequentially without a flag. Stage 5 gate 2 and the v0.1.0 tag depend on Phase 7's output only; no other stage depends on this plan.
