# Plan: Stage 2, less glue code

**Status: Implemented.** Sequences [prd-stage2-less-glue-code](../specs/prd-stage2-less-glue-code.md) (roadmap Stage 2) into 6 phases. A plan decides nothing: every behaviour below is fixed by that PRD; the "Decisions" section lists only the sequencing and implementation-shape calls this plan makes within the PRD's room, each with a one-line rationale. Built against `doc/rules/architecture.md` (incl. "Idiomatic Go"), `doc/rules/testing.md` and `doc/rules/specs.md`.

Conventions found: architecture, testing and spec rules in `doc/rules/`; guard tests `architecture_test.go` (import boundaries), `docs_test.go` (every exported identifier documented), `leak_test.go` (`TestNoDomainVocabulary`); `justfile` targets `test`, `test-race`, `lint`. No plan-format convention exists in the repo (`doc/plans/` is new), so the generic plan template is used.

## Scope

**In scope** (PRD U1-U3, M1-M4):
- `spread.Typed`, `spread.TypedEncounter`, and `spread.TypedTransform` (the last gated, see Phase 2).
- `declare.Attributes`, `declare.AttributeNames`, `declare.DefaultAttributeNames`, `(*Catalog).RegisterAttributeOps` and the three conditions it registers.
- `declare.ArgNames`, `declare.DefaultArgNames`, `Bindings.Discriminators`, `Bindings.ArgNames`, and the `discriminator` / `discriminator_from` args on `add_fact`, with the single allow-list helper.
- Ward rewrite (M1), a new data-file e2e example, fuzz seeds, `Example_` tests, README snippets, spec/CHANGELOG/index updates.

**Out of scope** (all per PRD; none is "real deferred work" in this plan):
- Numeric threshold conditions, data-set discriminators for `infer.Product`, a discriminator query in `knows`, typed rules actions/conditions, shipped attribute vocabulary, any change to `knowledge` (store, merge policy, config).
- Stage 1 (`Transform` signature, `Fade`): a prerequisite for Phase 2 only, not planned here.
- Stage 3 recipes, Stage 4 and Stage 5 are not scheduled here; see "Gated / not scheduled".

## Gated / not scheduled

| Item | Gate | What this plan does about it |
| --- | --- | --- |
| `TypedTransform` (Phase 2) | Stage 1 has landed the `Spreader.Transform` signature `func(e Encounter, delivered knowledge.Fact) knowledge.Fact` in the repo. Today the field is `func(knowledge.Fact) knowledge.Fact` (`spread/spread.go:123`). | Phase 2 starts with a mechanical check (Phase 2 step 1). If not met, Phase 2 is skipped (not stubbed) and the rest of the plan ships; M2 is satisfied by `Typed` alone. Phase 2 is then a follow-up the day Stage 1 lands. |
| Stage 3 recipes and second-consumer task | Needs Phases 1, 3, 4 landed (recipes compile against them). Second consumer is gated on a real outside project. | Nothing scheduled. Phases 1-5 keep names stable as Stage 3's assumption A1 requires. |
| Stage 5 `ArgNames.Roles` / `roles` arg | Stage 5 Unit C accepted and gated on real need. | Nothing built. Phase 4 only guarantees the extension seam: single allow-list helpers, zero-value-means-default, union rule documented. |
| Stage 4 | Measured need. | None. No benchmark added (PRD NFR). |

## Assumptions this plan makes (flagged, not decided)

- A1. One commit per phase, spec edits in the same commit as the code (specs rule 5). The plan does not run `git commit`; the executor commits when the owner asks.
- A2. Stage 1 may or may not have landed when execution starts (the working tree shows it has not). Phases 1, 3, 4, 5, 6 do not depend on it.
- A3. `logic.Condition[C]` stays `type Condition[C any] func(ctx C) bool` (`logic/logic.go:14`), so `Typed` can return a nil `logic.Condition[Encounter]`.
- A4. The new e2e data file is a new `declare/testdata/office_depts.json` rather than an edit to `office.json`, so `Example_office` output is untouched.
- A5. `config.md` needs no edit: the two names structs are wiring, not config (PRD R2.2b, R4.1). Phase 6 confirms by grep that no new `json`-tagged field entered `declare.Config`.

## Reuse findings (explored before drafting)

| Need | Existing code to extend, not reinvent |
| --- | --- |
| Name registration with reserved/duplicate/empty checks | `checkName`, `reserved`, `RegisterCondition` in `declare/catalog.go` |
| Arg parsing with typed errors, unknown-arg detection | `Args.String/Strings/BoolOr/Unknown` in `declare/args.go`; `ErrBadArgs`, `ErrBadBindings`, `ErrDuplicateName` in `declare/declare.go` |
| Optional-registration pattern, `Bindings`, `addFact`, `knows` | `declare/knowledgeops.go` (`RegisterKnowledgeOps`, `knows`, `addFact`) |
| Path-bearing load-time errors | `Catalog.condition/action` already wrap factory errors as `path: "use": err`; factories only return `ErrBadArgs`-wrapped errors |
| Filter composition and nil-filter handling | `logic.Condition`, `spread.WithFilter` (nil ignored, spec A5) |
| Ward e2e and fuzz scaffolding | `declare/ward_test.go`, `declare/testdata/ward.json`, `declare/fuzz_test.go` (`FuzzNode`, `testCatalog`, `encounterCatalog`), `declare/helpers_test.go` (`being`, `scene`) |
| Doc guards | `docs_test.go`, `architecture_test.go` (`declare` may import `spread`; core must not import `declare`), `leak_test.go` |

No new package, no new dependency, no new sentinel (PRD R4.2, R4.4). New files: `declare/attributes.go`, `declare/argnames.go` (or inside `knowledgeops.go` if under ~40 lines; executor's call), and tests beside them.

## Phase 1 - Typed spreading filter (`spread.Typed`)

**Why first:** no dependencies, smallest, highest value-for-effort (PRD order U1, U2, U3), and it unblocks Stage 3 recipe R2. Does not need Stage 1.

Usage first (architecture "Idiomatic Go" rule 1): write `Example_typedFilter` (S1, an office of `*Clerk` holders, same-floor filter, no type assertion in the body) and the README snippet text before the implementation; adjust until the call site reads well.

- `spread/typed.go`: `TypedEncounter[H Holder]` with `Teller, Listener H; Fact knowledge.Fact; Now knowledge.Tick` (R1.2) and `Typed[H Holder](f func(TypedEncounter[H]) bool) logic.Condition[Encounter]` (R1.1).
- Behaviour: `nil` f returns a nil condition (R1.5); teller or listener failing `e.Teller.(H)` returns false without calling `f` (R1.3, fail closed); `H` may be a pointer or interface type (R1.4); a nil `Holder` fails the assertion, so no panic.
- Doc comments carry R1.6 (no state held; concurrency is that of `f`) and the fail-closed rule.
- Mirror guard test: a reflect test asserting every field of `Encounter` has a same-named field in `TypedEncounter` (with `Holder` replaced by `H`), so a later stage adding a field to `Encounter` fails here (PRD cross-stage item 2).
- Spec edit, same change (Stage 1's Phase 1 edits to A2/A5/A8 come first; re-read A2 and A8 before editing and build on Stage 1's text): `doc/specs/spreading-and-inference.md` A2 (add `Typed`, `TypedEncounter`; replace the sentence "type-asserts `e.Teller`/`e.Listener`" with the helper), A5 (type mismatch is blocked, not an error), and A8 spaceport/office snippets only where they show a hand assertion for illustration (leave the `disposition <= -50` filter caller-written per PRD).

**Tests (pyramid):** unit, table-driven, in `spread/typed_test.go` (R1.1-R1.5, S2): matching types reach `f`; wrong teller; wrong listener; nil holder; interface-typed `H`; `Typed(nil)` ignored by `WithFilter`; composes under `logic.Not` and `logic.All`; `f` not called on mismatch (counter). Integration: `Example_typedFilter` with a real `Spreader` and `Store`s. No e2e at this level. Fakes, no mocks; no RNG or clock needed (deterministic pure function).

**Observability & rollback:** N/A (library, no logging by design). Rollback: revert the commit; the change is purely additive.

**Acceptance:** `just test`, `just test-race`, `just lint` clean; `TestNoDomainVocabulary`, `TestImportBoundaries`, docs guard pass; `Example_typedFilter` prints expected output with no `.(` assertion in the filter body; spec A2/A5 updated in the same change.

## Phase 2 - `spread.TypedTransform` (GATED on Stage 1)

**Why second:** shares `TypedEncounter` with Phase 1; separated so Phase 1 ships even if Stage 1 has not landed.

1. **Gate check (blocking step):** confirm `spread/spread.go` declares `Transform` as `func(e Encounter, delivered knowledge.Fact) knowledge.Fact` (or the named `type Transform func(...)` with field `Transform Transform`) and `Encounter` is unchanged. If it is still `func(knowledge.Fact) knowledge.Fact`, stop: record "Phase 2 deferred pending Stage 1" in the final report and skip to Phase 3. Do not edit `Spreader.Transform` here (that is Stage 1's change). If Stage 1 changed the signature differently from the PRD's cross-stage item 1, stop and report; do not adapt silently.
2. Usage first: `Example_typedTransform`.
3. `TypedTransform[H Holder](f func(TypedEncounter[H], knowledge.Fact) knowledge.Fact) func(Encounter, knowledge.Fact) knowledge.Fact` (R1.7). `TypedEncounter.Fact` is the teller's pre-decay fact (as `Encounter.Fact`). Mismatch returns `delivered` unchanged and does not call `f` (R1.8). `nil` returns nil (R1.9). The existing restore-fixed-fields rule in `Spread` still applies to what `f` returns (no change to that code).
4. Spec edit, same change: `spreading-and-inference.md` A2 (add `TypedTransform` next to the Stage 1 `Transform` text; do not rewrite Stage 1's text).

**Tests:** unit table (R1.7-R1.9, S13): match calls `f` and its result is delivered; wrong teller/listener returns `delivered` unchanged; nil leaves `Spreader.Transform` unset; `f` returning a changed `ID`/`Type` is still restored (drives a real `Spreader.Spread`, which is an integration-level assertion in a unit-sized test); assignability to the `Transform` field is a compile-time assertion in the test. Integration: `Example_typedTransform`.

**Observability & rollback:** N/A; revert the commit.

**Acceptance:** gate check recorded; tests/vet/race clean; `Example_typedTransform` output matches; spec updated. If gated out: the final report says so and no code for this phase exists.

## Phase 3 - Attribute conditions for data files (U2)

**Why third:** independent of U1/U3, medium risk because value-equality and missing-attribute semantics become permanent API; doing it before U3 keeps `knowledgeops.go` free of concurrent edits, and Phase 5's ward rewrite needs it.

Usage first: write `Example_attributes` (S3/S4-style rule and filter over a neutral office domain) and the README snippet text first.

Deliverables in `declare/attributes.go`:
- `Attributes[C any]{Sides []string; Lookup func(C, side, key string) (any, bool)}` (R2.1); missing/empty either returns `ErrBadBindings`.
- `AttributeNames{Equals, In, Same string}` with `json`/`yaml` tags `equals`, `in`, `same`; `DefaultAttributeNames()` (`attr_equals`, `attr_in`, `attr_same`); empty field = default; unexported `resolved()` fills defaults (R2.2a).
- `(*Catalog[C]).RegisterAttributeOps(a Attributes[C], n AttributeNames) error` (R2.2): validates bindings; resolves names; equal resolved names return `ErrDuplicateName`; registers all three or none (see Decision D2). Reserved/empty-name errors come from the existing `checkName`.
- Three factories, each ending with `Args.Unknown(...)` through one allow-list per condition (R2.9). Arg keys fixed (PRD D13): `attr_equals{side,key,value}`, `attr_in{side,key,values}`, `attr_same{side,key,other_side,other_key?}`. `side` is checked against a private copy of `Sides` at factory time, so a typo is a path-bearing `ErrBadArgs` at load (R2.8). `value` must be present (any JSON type, including null); `values` must be a non-empty slice of any kind (reflect slice check, so `[]any` from JSON/YAML and `[]string` from Go both work).
- `attrEqual(a, b any) bool` (R2.7): both numeric kinds (any int, uint, float) compare as float64; otherwise `==` guarded by a `reflect` comparability check plus a `recover` for the interface-field-holding-slice edge, returning false (never panics). Known limit, documented with a `shortcut:` comment: integers above 2^53 lose precision under float64 normalisation (PRD D11); upgrade only if a consumer reports it.
- Missing attribute (`ok == false`) makes every condition false; `not` over it is true; doc comment says so (R2.6).
- Doc comments: `Lookup` is the caller's only glue and its `key` argument comes from a data file, so treat it as untrusted input (never reflect into arbitrary fields by key). Not safe for concurrent use beyond `Lookup` itself; the registered closures hold no mutable state.
- Spec edit, same change: `doc/specs/declarative.md` new subsection "Attribute conditions (optional)" after "Knowledge operations" (type, names table, args, equality and missing semantics, errors), section 3 (errors reused, no new sentinel), section 5 (tests), and the sentence "Everything else ... is registered by the caller" amended to point at attribute ops for plain comparisons.

**Tests:** unit, table-driven, in `declare/attributes_test.go`: registration (R2.1, R2.2, R2.2a/b: zero names register defaults; custom names register and defaults then resolve to `ErrUnknownName`; two equal names, a pre-existing name, and a reserved name each fail; failed registration leaves the catalog unchanged; S14 rename half); semantics (R2.3-R2.7, S5, S6: missing key, int vs float64 vs JSON number, bool, nil, slice-valued attribute no panic, struct-with-interface-field no panic, `other_key` default); load errors (R2.8, R2.9, S7: unknown side with path, missing required arg, empty `values`, unknown arg; nothing registers when load fails); R2.10 (one `Catalog[spread.Encounter]` test, one rules-context test over `scene`); default-value test with override changes behaviour (testing rule 4). Integration: `Example_attributes` (a filter over real `Spreader`, a rule over real `rules.Registry`). Race: `-race` on a test calling the registered conditions from several goroutines with a read-only `Lookup`.

**Observability & rollback:** diagnosability is load-time errors with node paths (asserted in tests). Rollback: revert the commit; no data format in use depends on the new names yet (the ward file moves only in Phase 5).

**Acceptance:** all R2.x tests pass; `Example_attributes` prints expected output; `just test`, `just test-race`, `just lint` clean; leak and import guards pass (`reflect` is stdlib, allowed); spec updated in the same change.

## Phase 4 - Identity control from data (U3)

**Why fourth:** it is the last item the PRD would cut, but it touches `Bindings` and the `add_fact`/`knows` allow-lists that Stage 5 later extends, so it is built once, carefully, after the simpler additions and before the ward proof that exercises it.

Usage first: write `Example_discriminator` (S8: two victims, two facts; same victim, one merged fact).

Deliverables in `declare/knowledgeops.go` (plus `declare/argnames.go` if split):
- `ArgNames{Discriminator, DiscriminatorFrom string}` with tags `discriminator`, `discriminator_from`; `DefaultArgNames()`; unexported `resolved()` (R3.1a). Doc: shared by `add_fact` and `knows`, extended only by adding fields, zero field means default.
- `Bindings[C]` gains `Discriminators map[string]func(C) string` and `ArgNames ArgNames`; existing fields untouched (R3.1).
- `RegisterKnowledgeOps` validates: any nil entry in `Discriminators` is `ErrBadBindings`; resolved arg names that are equal to each other, or collide with a base arg of `add_fact` (`type`, `source`, `confidence`, `ttl`, `private`, `per_event`) or `knows` (`type`, `about_subject`, `min_confidence`, `source`), are `ErrBadBindings` (R3.9). It keeps a private copy of the `Discriminators` map.
- One unexported allow-list helper per action (`addFactArgs(ArgNames) []string`, `knowsArgs(ArgNames) []string`); `addFact` and `knows` call `a.Unknown(helper(...)...)` and nothing else lists arg names. Doc comment states the union rule (R3.9). `knowsArgs` has no optional names yet; it exists so Stage 5 appends in one place.
- `addFact` parsing: `discriminator` via `Args.String`; `discriminator_from` via `Args.String`, resolved at load against the private map, unknown source is `ErrBadArgs` listing sorted registered names (R3.4); any two of `discriminator`, `discriminator_from`, `per_event(true)` together is `ErrBadArgs` (R3.5). At run time the fact's `Discriminator` is the literal, the source's result (empty string yields the default identity, R3.6), or the event ID for `per_event` (unchanged). `Store.Add` keeps rejecting U+001F (`ErrInvalidFact`, R3.7); no duplicate check.
- Spec edit, same change: `declarative.md` Knowledge operations section (Bindings struct, `ArgNames`, the `add_fact` table row, mutual-exclusion and empty-result rules, union-rule note) and `knowledge.md` only if it states that `per_event` is the sole data-file way to set identity (grep; edit that sentence, no semantic change to `knowledge`).

**Tests:** unit, table-driven, `declare/knowledgeops_test.go` additions: literal; `from`; unknown source lists names (S10); every mutual-exclusion pair and `per_event` alone unchanged (S9, existing regression tests untouched); empty source result merges with default identity (R3.6); U+001F literal surfaces `ErrInvalidFact` at run time (R3.7); two discriminators give two facts, same gives one merged (R3.8); zero `ArgNames` uses defaults, renamed args work and old names then fail `Unknown` with `ErrBadArgs`, collisions and nil source entries return `ErrBadBindings` (R3.1a); defaults test plus override-changes-behaviour (rule 4); S14 second half (house-style names). A small internal test asserts each helper's list contains all base args plus the configured optional names. Integration: `Example_discriminator`.

**Observability & rollback:** load-time errors carry paths (existing wrapping). Rollback: revert; `Bindings` zero values keep behaviour, so partial rollback is not needed.

**Acceptance:** all R3.x tests pass; existing `per_event` and `add_fact` tests pass unmodified; `just test`, `just test-race`, `just lint` clean; spec updated in the same change.

## Phase 5 - Ward rewrite, office e2e, fuzz (M1, S4, S11, S12)

**Why fifth:** needs Phases 3 and 4 and proves them end to end.

- Ward: delete `role_is` and `listener_role_is` registrations and `roleIn` from `declare/ward_test.go`; register `RegisterAttributeOps` for `Catalog[call]` (side `entity`) and `Catalog[spread.Encounter]` (sides `teller`, `listener`) with `Lookup` closures over `staff.role`. The ward has two context types, so it needs one `Lookup` per catalog (they may share a small helper); update `ward.json` to `attr_in` with `side`/`key`/`values`. `Example_ward` output must be byte-identical. Count non-blank lines in the "ward setup" region before and after; the number must drop. If it does not, report it honestly in `declarative.md` section 7 rather than reshaping the code to hit a number.
- New `declare/testdata/office_depts.json` + `Example_officeDepts` (S4: `not(attr_same{side: teller, other_side: listener, key: department})` as a spreading filter, plus an `attr_equals` rule per S3). Uses `being.attrs` from `helpers_test.go` as the attribute source.
- Fuzz: extend `testCatalog` and `encounterCatalog` in the fuzz/helpers tests to register attribute ops and a `Discriminators` entry; add seeds to `FuzzNode` for the new names (`attr_equals` with `value` of each JSON type, `attr_in` with empty/mixed `values`, `attr_same` with missing `other_side`, `add_fact` with `discriminator_from`, both discriminator args). `FuzzLoad` already seeds every file in `testdata/`.
- Spec edit, same change: `declarative.md` section 6 (new example listed), section 7 (ward line counts and the sentence that no library change was needed, now with the before/after setup count and the note that the ward now uses built-in attribute ops).

**Tests:** e2e (data file through `Load`, wiring, run): `Example_ward`, `Example_officeDepts`. Fuzz: `FuzzNode` (run a short `-fuzztime` locally, e.g. 30s, as a smoke check; seeds run under plain `go test`). Unit coverage already in Phases 3-4.

**Observability & rollback:** none beyond test output. Rollback: revert; Phases 3-4 stand alone.

**Acceptance:** `Example_ward` output unchanged; setup line count recorded; `Example_officeDepts` passes; fuzz seeds pass and a 30s fuzz run finds no panic; `just test`, `just test-race`, `just lint` clean.

## Phase 6 - README, CHANGELOG, index, final verification (M2-M4)

**Why last:** documents the final shipped names once; cut-first per PRD priority (README snippets), but the CHANGELOG and index edits are required.

- `README.md`: in "Spreading and inference" add the `spread.Typed` snippet (the M2 example, fewer lines than the hand-asserted form, no explicit type assertion); in "Declarative loading" add a short attribute-conditions and `discriminator_from` snippet. Neutral domain only (testing rule 3).
- `CHANGELOG.md`: an `Unreleased` "Added" entry listing the new exported identifiers (`Typed`, `TypedEncounter`, `TypedTransform` if Phase 2 ran, `Attributes`, `AttributeNames`, `DefaultAttributeNames`, `RegisterAttributeOps`, `ArgNames`, `DefaultArgNames`, `Bindings.Discriminators`, `Bindings.ArgNames`, `add_fact` args `discriminator`, `discriminator_from`).
- `doc/README.md`: mark the Stage 2 PRD line implemented (or "implemented except TypedTransform, pending Stage 1" if Phase 2 was gated out); list `doc/plans/plan-stage-2.md` in the index (repo rule: update the index with every new doc).
- PRD status line set to implemented, and the setup line count recorded where M1 asks for it (`declarative.md` section 7, already done in Phase 5).
- Final verification: `just test`, `just test-race`, `just lint`; grep that `declare.Config` gained no field; M1-M4 checklist walked against the PRD acceptance table.

**Tests:** README snippets are covered by the `Example_` tests they mirror (copy, do not paraphrase). No new tests.

**Observability & rollback:** docs only; revert.

**Acceptance:** every PRD acceptance-table row is mapped to a passing test in the final report; docs index lists the plan; all three `just` targets clean; no file outside the listed ones changed (`git diff --stat` review).

## Security review

One pass across the new trust boundaries:
- **Data file to `Lookup`.** `side` and `key` strings come from a data file. `side` is validated against the caller's `Sides` at load; `key` is not validated (PRD D2) and goes to the caller's `Lookup`. A data file can therefore only reach what `Lookup` chooses to expose; the doc comment tells implementers to treat `key` as untrusted (no reflection by key name, no map-of-everything). No expressions or interpolation are introduced.
- **Panics.** The new library code does not panic on any data-file value or holder type: failed type assertions fail closed, non-comparable values compare unequal (comparability check plus `recover` guard), nil `Discriminators` entries are rejected at registration, and a nil `Lookup` at registration. Panics from caller `Lookup` or discriminator functions propagate, as for other caller hooks (PRD NFR).
- **Fail-closed filter.** `Typed` blocks on a type mismatch, so a mixed-holder spreader never leaks a fact through a typed restriction by accident. `TypedTransform` passes through, which cannot widen delivery.
- **Identity forgery.** `discriminator`/`discriminator_from` set only `Fact.Discriminator`; `ID`, `Type`, `Subject`, `Source`, `Prov` are unchanged, and `Store.Add` still rejects U+001F. A data file cannot make a fact overwrite another type's or subject's fact, because those remain part of identity.
- **Resource exhaustion.** `attr_in` is O(len(values)) per evaluation with values fixed at load; a data file author already controls rule count. `discriminator_from` with a high-cardinality source creates many facts, bounded by `knowledge.Config.MaxFacts` (documented in the `discriminator_from` text, as Stage 3 recipe R3 also states).
- **Aliasing.** `Sides` and `Discriminators` are copied at registration so later caller mutation cannot race with concurrent condition evaluation.
- No secrets, PII, network or file access are added.

## Compatibility & rollout

Pure addition on a `v0.x` library. `Bindings` gains optional fields (zero value keeps behaviour); `add_fact` gains optional args (existing data files load and behave identically; `per_event` unchanged); `Catalog` gains one method; `spread` gains new identifiers. The only behavioural edge is that `discriminator` and `discriminator_from` on `add_fact`, previously load errors (unknown arg), become valid under the default names; when a consumer renames them via `ArgNames`, the default names are unknown again, which is the intended rename semantics (S14). The ward test data changes (test-only). Phases are independently revertible; no expand-contract is needed because nothing is removed. Dependency failure during rollout: not applicable (no external dependency, standard library only). Stage 1 dependency is contained by the Phase 2 gate.

## Decisions

Made under the lead's principles (open, idiomatic, smallest exported surface, injected policy, no hardcoded vocabulary, tunables as config). PRD decisions D1-D14 are honoured unchanged.

| # | Decision | Rationale |
| --- | --- | --- |
| D1 | `TypedTransform` is its own gated phase after `Typed`, and Phase 2 is skipped (not stubbed) if Stage 1 has not landed. | The signature it adapts does not exist in the tree yet; stubbing would ship an unusable API. |
| D2 | `RegisterAttributeOps` registers all three names or none, via a small unexported multi-register helper in `catalog.go` that checks every name under one lock before inserting. Existing methods and behaviour are unchanged. | The Catalog has no unregister, and a half-registered set on a duplicate name is a trap; the PRD's "no change to Catalog internals" is read as "no behaviour change". Flagged for override. Deviation: RegisterAttributeOps does the all-or-none check inline under the catalog lock rather than via a catalog.go helper. |
| D3 | `Sides` and `Discriminators` are copied at registration. | Instance-owned state, safe against caller mutation (architecture rules 5, 10). |
| D4 | A nil function in `Bindings.Discriminators` is `ErrBadBindings` at registration. | Otherwise a nil call panics at run time (rule 12); reuses the existing sentinel (R4.4). |
| D5 | `ArgNames` renames are collision-checked against the base args of both `add_fact` and `knows`, and against each other. | Stage 5 extends the same struct for `knows`; checking now avoids a later breaking rename error. |
| D6 | Equality uses a reflect comparability check plus a `recover` guard, returns false on any failure. | Go `==` on an interface holding a slice panics even when the static type looks comparable; rule 12 forbids panics. |
| D7 | `values` accepts any slice kind via reflect. | JSON/YAML give `[]any`, Go callers give typed slices; keeps data files and Go in agreement without a type switch per kind. |
| D8 | `Lookup`'s `key` is documented as untrusted data-file input. | The only injection surface; costs one doc sentence. |
| D9 | A reflect test keeps `TypedEncounter` field-for-field in step with `Encounter`. | Turns the PRD's cross-stage assumption 2 into a failing test instead of a comment. |
| D10 | A new `office_depts.json` is added instead of editing `office.json`. | Leaves `Example_office` untouched and `FuzzLoad` seeds auto-extend. |
| D11 | Ward uses one `Lookup` per context type (two), and the line-count claim is reported as measured, not forced. | Two catalogs over two context types cannot share one closure; honesty over the M1 wording. |
| D12 | `Example_` tests and README snippet text are written before each implementation. | Architecture "Idiomatic Go" rule 1. |
| D13 | Phase order U1, U1b (gated), U2, U3, proof, docs. | PRD recommended order; the proof needs U2 and U3. |

## Cross-stage assumptions (exact names)

- From Stage 1: `spread.Spreader.Transform` is `func(e Encounter, delivered knowledge.Fact) knowledge.Fact` (possibly named `spread.Transform`); `spread.Encounter{Teller, Listener Holder; Fact knowledge.Fact; Now knowledge.Tick}` unchanged; fixed-field restore list unchanged. `knowledge.Fact` gains `FadedAt Tick` (not touched here).
- To Stage 3: `spread.Typed`, `spread.TypedEncounter`, `declare.Catalog.RegisterAttributeOps(declare.Attributes[C], declare.AttributeNames) error`, `declare.DefaultAttributeNames()` (`attr_equals`, `attr_in`, `attr_same`), `declare.Bindings.Discriminators`, `declare.Bindings.ArgNames`, `declare.DefaultArgNames()`, data args `discriminator`, `discriminator_from`; mutual exclusion with `per_event`.
- To Stage 5: `declare.ArgNames` is additive (Stage 5 adds `Roles`); allow-lists come from `addFactArgs`/`knowsArgs`; renames that collide are `ErrBadBindings`.
- Unchanged and relied on: `knowledge.Store.Add` (U+001F rejection, `ErrInvalidFact`), identity `Type`, `Subject`, `Discriminator`, `logic.Condition[C]`, `spread.WithFilter`, `declare.Args.Unknown`, `ErrBadArgs`, `ErrBadBindings`, `ErrDuplicateName`, `ErrUnknownName`.

## Specs updated in the same change as code

- `doc/specs/spreading-and-inference.md`: Phase 1 (A2, A5, A8), Phase 2 (A2).
- `doc/specs/declarative.md`: Phase 3 (attribute conditions, errors, tests), Phase 4 (Knowledge operations), Phase 5 (sections 6 and 7).
- `doc/specs/knowledge.md`: Phase 4, only if it claims `per_event` is the sole data-file identity control.
- `doc/specs/config.md`: unchanged by design (A5); confirmed in Phase 6.
- Also: `doc/README.md`, `CHANGELOG.md`, `README.md` (Phase 6).
