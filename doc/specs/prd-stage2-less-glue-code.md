# PRD: Stage 2, less glue code

Status: implemented · Owner: lead agent (rumour_mill) · Last updated: 2026-10-18 · Sign-off: repo owner (Andy Beak)

Roadmap source: `doc/roadmap.md`, Stage 2. Builds on `spread` and `declare` as specified in [spreading-and-inference](spreading-and-inference.md), [declarative](declarative.md) and [config](config.md). Conventions followed: `doc/rules/architecture.md` (incl. "Idiomatic Go"), `doc/rules/testing.md`, `doc/rules/specs.md`. No design-system doc exists and the library has no UI, so the UX section is N/A. Tags: **[D]** decided (by a doc, the code, or the lead's brief). Every item is now decided; see Decisions.

## Problem & Goal

The end user of this library is a game or simulation developer wiring a new setting. Today, three common needs force them to write Go that has no setting-specific logic in it:

1. A spreading filter must type-assert `e.Teller` and `e.Listener` to the caller's holder type by hand (`spread/` spec A2, spaceport example). Every filter repeats the assertion, and a wrong assertion panics.
2. Relational rules ("listener has role X", "teller is in the same department as the listener", "the entity is on the same floor as the event") each need a hand-written named condition, although each is only an attribute comparison. The hospital-ward generality proof needs two such one-liners (`role_is`, `listener_role_is`) for 3 rules and 1 filter (`declare/ward_test.go`).
3. `add_fact` can only merge repeats (default) or separate them per event (`per_event`). A data file cannot say "one fact per victim" or "keep these two rules' claims apart", so such rules need a custom Go action (`knowledge` spec 3.1: identity is `Type, Subject, Discriminator`).

Goal: a new setting needs fewer lines of Go to wire up its data, without the library gaining any vocabulary, state or tunables.

Success measures:
- **M1.** The ward proof (`declare/testdata/ward.json` + `declare/ward_test.go`) is rewritten so its `role_is` and `listener_role_is` registrations are deleted and replaced by built-in attribute conditions plus one `Lookup` function; non-blank setup lines go down, and behaviour (the example output) is unchanged.
- **M2.** A new `Example_` shows a spreading filter written with the typed helper in fewer lines and with no explicit type assertion.
- **M3.** A data-only rule can create one fact per distinct value of a caller-named key, shown in an `Example_` and tested.
- **M4.** No new config field, no global state, no import-boundary or leak-guard regressions.

## Scope

### In scope
- **U1 Typed spreading hooks:** generic helpers in `spread` for filters (`Typed`) and for the `Transform` hook (`TypedTransform`).
- **U2 Attribute conditions for data files:** a new optional registration on `declare.Catalog`, following the `Bindings` / `RegisterKnowledgeOps` pattern.
- **U3 Identity control from data:** `add_fact` gains arguments to set the discriminator, backed by caller-named discriminator sources.
- Spec updates (this PRD becomes the basis for `doc/specs/` edits in the same change as the code), README snippet, `Example_` tests, fuzz coverage of the new names.

### Out of scope
- Numeric comparisons (`>=`, `<=`) over attributes. **[D]** The spaceport "disposition ≤ -50" filter stays caller-written until a consumer needs thresholds.
- Setting `subject`, `Fact.ID`, or the discriminator from data in **inference** products (`infer.Product`). **[D]**
- A discriminator query in `knows` (`knowledge.Query` has no discriminator).
- Typed actions / typed conditions for rules (the rules `C` is already a caller type, so generics already give typing there).
- Any shipped attribute names, side names, or fact vocabulary; any schema for attributes; a world model (architecture rules 1, 3).
- A new store, merge policy, or change to merge semantics.

### Priority
Must: U1 `Typed`, U2 (three attribute conditions, renameable), U3 (`discriminator`, `discriminator_from`, renameable). Should: `TypedTransform` (promoted by the lead once Stage 1 confirmed the `Transform` signature), README snippets, ward rewrite (M1). If cut for time, cut README snippets and `TypedTransform` first, then the ward rewrite; U3 is last to cut because it removes a whole class of custom actions.

## Requirements

Each states one behaviour; all trace to the end user above.

### U1 Typed spreading filters (package `spread`)
- **R1.1** `spread.Typed[H Holder](f func(TypedEncounter[H]) bool) logic.Condition[Encounter]` returns a condition usable with `WithFilter` and `logic.All/Any/Not`. **[D]** generic helper, per lead brief.
- **R1.2** `TypedEncounter[H]` has fields `Teller, Listener H`, `Fact knowledge.Fact`, `Now knowledge.Tick`: the same shape as `Encounter` with `H` in place of `Holder`.
- **R1.3** If `Teller` or `Listener` is not an `H`, the condition returns `false` (the encounter is blocked) and `f` is not called. Never panics. **[D]** fail-closed is the safe default for a filter whose job is to restrict.
- **R1.4** `H` may be a pointer type or an interface satisfying `Holder`; the assertion is a plain Go type assertion on the dynamic value.
- **R1.5** `Typed(nil)` returns a nil condition, which `WithFilter` already ignores (spec A5, "a nil filter is ignored").
- **R1.6** Doc comment states: not safe for concurrent use beyond what `f` is; no state held.
- **R1.7** `spread.TypedTransform[H Holder](f func(TypedEncounter[H], knowledge.Fact) knowledge.Fact) func(Encounter, knowledge.Fact) knowledge.Fact` returns a value assignable to `Spreader.Transform` (Stage 1 signature: `func(e Encounter, delivered knowledge.Fact) knowledge.Fact`, `Encounter` unchanged). `TypedEncounter.Fact` is the teller's fact before decay (as `Encounter.Fact`); the second argument is the copy about to be delivered. **[D]**
- **R1.8** If `Teller` or `Listener` is not an `H`, `TypedTransform` returns `delivered` unchanged and does not call `f` (a transform cannot block; "no distortion" is the neutral outcome). **[D]**
- **R1.9** `TypedTransform(nil)` returns nil, which leaves `Spreader.Transform` unset. The existing restore-fixed-fields rule (spec A2) still applies to what `f` returns.

### U2 Attribute conditions (package `declare`)
- **R2.1** New type `Attributes[C any]` with `Sides []string` (required, non-empty: the side names a data file may use) and `Lookup func(C, side, key string) (any, bool)` (required). Missing either returns `ErrBadBindings` from registration.
- **R2.2** `(*Catalog[C]).RegisterAttributeOps(a Attributes[C], n AttributeNames) error` registers exactly three condition names, taken from `n`. It returns `ErrDuplicateName` if a name is already registered or if two of the three names are equal.
- **R2.2a** `AttributeNames` is a small exported config struct with fields `Equals`, `In`, `Same` (string, with `json`/`yaml` tags `equals`, `in`, `same`). `DefaultAttributeNames()` returns `{Equals: "attr_equals", In: "attr_in", Same: "attr_same"}`. The zero `AttributeNames{}` is valid: each empty field takes its default. A name that is non-empty but reserved (`all`, `any`, `not`, `seq`, `if`) is rejected by the existing `RegisterCondition` check. This lets a consumer rename to avoid collisions or match house style; the library ships no fixed vocabulary beyond these overridable defaults. **[D]** (lead decision)
- **R2.2b** `AttributeNames` can be embedded in a data file by a consumer if they choose; it is not added to `declare.Config`/`Document` (names are wiring, not behaviour tunables, and registration happens before the file's nodes are resolved). **[D]**
- **R2.3** `attr_equals` args: `side`, `key`, `value` (all required). True if `Lookup(c, side, key)` is present and equals `value`.
- **R2.4** `attr_in` args: `side`, `key`, `values` (non-empty list). True if the looked-up value is present and equals any element.
- **R2.5** `attr_same` args: `side`, `key` (required); `other_side` (required); `other_key` (optional, default `key`). True if both lookups are present and equal.
- **R2.6** A missing attribute makes the condition false. `not` over it is therefore true; this is documented so authors write `all(attr_in..., not(attr_equals...))` knowingly.
- **R2.7** Equality: numeric values of any Go integer or float kind compare by float64 value, so a YAML `int` equals a JSON `float64`; otherwise values compare with Go `==`; a non-comparable value compares unequal and never panics.
- **R2.8** `side` not in `Attributes.Sides` is an `ErrBadArgs` at **load time** (resolution), with the node path, so typos surface when the file loads, not mid-simulation.
- **R2.9** Each factory ends with `Args.Unknown(...)` so misspelt args fail loudly (existing convention).
- **R2.10** Works for any `C`, including `spread.Encounter` (filters) and a rules context.
- **R2.11** `Lookup` is the caller's only glue; the library ships no side names, keys or values. **[D]**

### U3 Identity control from data (package `declare`)
- **R3.1** `Bindings[C]` gains optional `Discriminators map[string]func(C) string`: named discriminator sources the caller provides (for example `"event"`, `"victim"`). Existing fields and behaviour are unchanged. **[D]** (see Cross-stage interfaces)
- **R3.1a** `Bindings[C]` gains optional `ArgNames ArgNames`, a small exported struct with fields `Discriminator` and `DiscriminatorFrom` (tags `discriminator`, `discriminator_from`). `DefaultArgNames()` returns those defaults; the zero value is valid (empty field = default). `ArgNames` is shared by `add_fact` and `knows`: it is only ever extended additively with new fields, and each field's zero value means its default (Stage 5 adds `Roles`, which applies to both actions). Renames apply only to these two new args; the existing `add_fact` args (`type`, `confidence`, `source`, `ttl`, `private`, `per_event`) keep their names. **[D]** (lead decision)
- **R3.2** `add_fact` gains an optional arg named by `ArgNames.Discriminator` (default `discriminator`, string literal): sets `Fact.Discriminator` to that constant.
- **R3.3** `add_fact` gains an optional arg named by `ArgNames.DiscriminatorFrom` (default `discriminator_from`, string): names a source in `Bindings.Discriminators`; the discriminator is that function's result for the context at run time.
- **R3.4** An unknown source name in `discriminator_from` is `ErrBadArgs` at load time, listing the registered names.
- **R3.5** `discriminator`, `discriminator_from` and `per_event` are mutually exclusive; combining any two is `ErrBadArgs` at load time. `per_event` keeps its present behaviour. **[D]** no breaking change.
- **R3.6** A source returning `""` yields an empty discriminator, so the fact merges with other discriminator-less facts of that type and subject (the default identity). Documented.
- **R3.7** A discriminator containing U+001F is rejected by `Store.Add` with `ErrInvalidFact` at run time (existing rule, not duplicated here); `add_fact` returns that error.
- **R3.9** The `Args.Unknown(...)` allow-list for `add_fact` (and for the three attribute conditions) is built in one unexported helper per name, as base args plus the configured optional arg names, so another stage can add args by extending that one list. The doc comment states the **union rule**: any stage that adds an arg to `add_fact` or `knows` (Stage 5 plans an optional `roles`) must add it to the same allow-list, never replace it. Renamed arg names are included in the list, and a rename that collides with another arg of the same action is `ErrBadBindings` at registration. **[D]**
- **R3.8** With the same `type`, `subject` and `discriminator`, repeats merge under the store's merge policy; with different discriminators they are separate facts (no change to `knowledge`).

### Cross-cutting
- **R4.1** No new tunables and no magic numbers. The only new configuration is the two names structs (R2.2a, R3.1a), each with a documented default and a valid zero value. **[D]**
- **R4.2** No new package-level state, `init()`, or dependency (standard library only; `reflect` is permitted in `declare` for R2.7). Core packages must not import `declare` (`architecture_test.go`).
- **R4.3** Every new exported identifier has a doc comment (`docs_test.go`); the leak guard passes.
- **R4.4** All new names resolve through the existing `Catalog` error paths with existing sentinels (`ErrBadArgs`, `ErrBadBindings`, `ErrDuplicateName`); no new sentinel. **[D]**

## Non-Functional Requirements

### Performance, Security, Reliability, Scale
- Conditions are called on the hot path of spreading and rule processing: each does at most one or two `Lookup` calls and no allocation beyond `Lookup`'s own. No benchmark is required (Stage 4 owns benchmarking).
- Reliability: none of the new code panics on bad data or bad types (library rule 12). Panics from caller `Lookup` or discriminator functions propagate, as for other caller hooks.
- Determinism: pure functions of their inputs; no randomness or clock.
- Security: data files stay data; no expressions or interpolation. Attribute names are looked up only through the caller's `Lookup`, so a data file cannot reach anything the caller does not expose.

### Privacy / Compliance
N/A: no personal data handled. The library's `Private` fact exclusion from spreading is unaffected.

### Observability
The library has no logging or metrics by design. Diagnosability comes from load-time errors with node paths (R2.8, R3.4, R3.5).

### Rollout & Release Safety
Pure addition, pre-v1 (`v0.x`). `Bindings` gains an optional field (zero value keeps behaviour) and `add_fact` gains optional args, so existing data files and Go callers compile and behave identically. Recorded in `CHANGELOG.md`. Rollback is reverting the change.

## UX & Design

N/A: no user interface. The "user experience" is the API and data-file ergonomics, covered by R2.8/R3.4 (errors at load) and by writing the `Example_` tests and README snippets first (architecture rule "Usage first").

## Stakeholders & Sign-off / Acceptance Criteria

Owner: lead agent. Approver: repo owner. Cross-stage reconciliation by the lead before any spec edit lands.

| Requirement | Acceptance criterion |
| --- | --- |
| R1.1-R1.5 | Table test in `spread`: matching types pass through to `f`; wrong teller type, wrong listener type, and a nil-store holder each yield `false`/behave per spec without panic; `Typed(nil)` is ignored by `WithFilter`; composes under `logic.Not`. |
| R1.7-R1.9 | Table test: matching types call `f` and its result is delivered; wrong teller/listener type returns `delivered` unchanged; `nil` leaves Transform unset; a returned changed ID is still restored. |
| R1.x usage | `Example_typedFilter` and `Example_typedTransform` compile and print expected output. |
| R2.2a-b | Test: `AttributeNames{}` registers the defaults; custom names register and resolve, defaults then do not; two equal names → `ErrDuplicateName`. |
| R3.1a, R3.9 | Test: zero `ArgNames` uses defaults; renamed args work and the old names are then unknown-arg errors; collision with an existing arg → `ErrBadBindings`; allow-list is a single list (code-review check). |
| R2.1-R2.2 | Test: nil `Lookup`, empty `Sides` → `ErrBadBindings`; second registration or pre-existing name → `ErrDuplicateName`. |
| R2.3-R2.7 | Table tests for equals / in / same incl. missing key, numeric normalisation (`int` vs `float64`), bool, non-comparable value, `other_key` default. |
| R2.8-R2.9 | Table tests for unknown side, missing required arg, empty `values`, unknown arg: each `ErrBadArgs` with a path. |
| R2.10 | One test over `Catalog[spread.Encounter]` and one over a rules context. |
| R3.1-R3.8 | Tests: literal, `from`, unknown source, mutual exclusion, empty source result, U+001F rejection, two discriminators = two facts, same = merged. `per_event` regression tests unchanged. |
| M1 | Ward rewrite passes with the same output; setup line count recorded in the spec's section 7. |
| R4.1 | Every new default (`DefaultAttributeNames`, `DefaultArgNames`) has a test asserting its value and that overriding changes behaviour (`testing.md` rule 4). |
| R4.x | `go vet ./...`, `go test ./...`, `go test -race ./...`, `TestNoDomainVocabulary`, `architecture_test.go`, `docs_test.go` pass. |

## Phases / Units

Each unit is independently shippable and has its own acceptance criteria above. Units are independent of each other; recommended order U1, U2, U3 by value-for-effort.

| Unit | Depends on | Risk | Decision vs assumption |
| --- | --- | --- | --- |
| **U1** typed filters and transform | none (Stage 1's `Transform` signature is confirmed) | Low: ~25 lines. Design choices: fail-closed filter (R1.3), pass-through transform (R1.8), single `H`. | Helper shape, fail-closed, single `H` **[D]**; transform pass-through **[D]** (R1.8) |
| **U2** attribute ops | none (reuses `Catalog`, `Args`) | Medium: value-equality semantics (R2.7) and missing-attribute semantics (R2.6) are subtle and become permanent API; reflect use. | Pattern, renameable names, required `Sides` **[D]**, numeric normalisation (R2.7), `AttributeNames` outside `Config` (R2.2b) |
| **U3** identity from data | none; touches `Bindings` which another stage may also touch | Medium: `per_event` coexistence; Stage 5 will extend the same `add_fact`/`knows` allow-list. | Intent and mechanism (named sources on `Bindings`) **[D]** |

## Testing / Key Scenarios

Pyramid levels per `doc/rules/testing.md` (no explicit pyramid; fallback): **unit** = table-driven in-package test; **integration** = `Example_` / multi-package test using real `Catalog`, `Spreader`, `Store`; **e2e** = data file through `Load` → wiring → run (the ward and a new office data file); **fuzz** = `FuzzNode`.

| # | Unit | Scenario | Level |
| --- | --- | --- | --- |
| S1 | U1 | Given an office with `*Clerk` holders, when a `Typed` filter allows only same-floor tellings, then a same-floor encounter delivers and a different-floor one does not, with no assertion in the filter body. | integration (Example) |
| S2 | U1 | Given a spreader whose listener list contains a holder of another type, when the typed filter runs, then that encounter is blocked and nothing panics. | unit |
| S3 | U2 | Given a rule `when: attr_equals{side: entity, key: role, value: night-shift}`, when an event is processed for a night-shift and a day-shift clerk, then only the first rule fires. | integration |
| S4 | U2 | Given a spreading filter `not(attr_same{side: teller, other_side: listener, key: department})`, when a clerk tells a colleague in another department, then it delivers; same department is blocked. | e2e (data file + Example) |
| S5 | U2 | Given an attribute missing on the listener, when `attr_equals` and `not(attr_equals)` are evaluated, then false and true respectively. | unit |
| S6 | U2 | Given `attr_equals{value: 3}` and a Go `int` attribute 3 and a loaded JSON number 3, then it is true; and a slice-valued attribute never panics. | unit |
| S7 | U2 | Given a data file naming side `evnt` (typo), when loaded, then it fails with a path-bearing `ErrBadArgs` and nothing registers. | unit |
| S8 | U3 | Given `add_fact{discriminator_from: "victim"}` and two events with different victims, when both rules run, then the store holds two facts; with the same victim, one merged fact. | integration (Example) |
| S9 | U3 | Given `discriminator` and `per_event` together, when loaded, then `ErrBadArgs`; `per_event` alone behaves as before. | unit |
| S10 | U3 | Given `discriminator_from` naming an unregistered source, then load fails listing the registered names. | unit |
| S11 | all | Given the ward data file with built-in attribute conditions, then its `Example_` output is unchanged and setup is shorter (M1). | e2e |
| S13 | U1 | Given a `TypedTransform` that adds embellishment only when the teller is a `*Clerk`, when a non-`Clerk` holder tells, then the delivered copy is unchanged. | unit |
| S14 | U2, U3 | Given names renamed to a house style (`is`, `is_one_of`, `matches`), when a data file uses the new names, then it loads; a file using the old names fails with `ErrUnknownName`. | unit |
| S12 | all | Arbitrary JSON through `Catalog.Condition/Action` including the new names never panics. | fuzz |

## Cross-stage interfaces

(Assumed interfaces, for the lead to reconcile.)

1. **`spread.Spreader.Transform`.** Stage 1 confirmed the signature `func(e Encounter, delivered knowledge.Fact) knowledge.Fact`, with `Encounter` unchanged. U1 adds `TypedTransform[H Holder](f func(TypedEncounter[H], knowledge.Fact) knowledge.Fact) func(Encounter, knowledge.Fact) knowledge.Fact`, assignable to the field. It does not change the field or its semantics (fixed fields still restored).
2. **`spread.Encounter`.** Assumed unchanged: fields `Teller, Listener Holder; Fact knowledge.Fact; Now knowledge.Tick`. `TypedEncounter[H]` mirrors it, so if any stage adds a field to `Encounter`, `TypedEncounter` must add the same one.
3. **Spreader filters.** Assumed unchanged: `WithFilter(logic.Condition[Encounter])`, all must pass. `Typed` returns exactly that type.
4. **`declare.Catalog`.** Adds one method, `RegisterAttributeOps(Attributes[C], AttributeNames) error`, registering three condition names taken from `AttributeNames` (defaults `attr_equals`, `attr_in`, `attr_same`; zero value valid). No change to `Catalog` internals or to `RegisterCondition/RegisterAction`. A consumer renames to avoid collisions with its own or another stage's built-ins (`knows`, `add_fact`).
5. **`declare.Bindings`.** Adds two optional fields: `Discriminators map[string]func(C) string` and `ArgNames ArgNames`. Existing fields (`Store, Now, Subject, EventID`) unchanged. If another stage also extends `Bindings`, its fields should be independent and optional.
6. **`add_fact` args.** Adds two args named by `Bindings.ArgNames` (defaults `discriminator`, `discriminator_from`); `per_event` stays and is mutually exclusive with them. **Union rule for the `Unknown(...)` allow-list** (R3.9): the allow-list for `add_fact` (and for `knows`) is one unexported list built from base args plus configured optional names; Stage 5's optional `roles` arg on `add_fact` and `knows` must be appended to that list, never replace it, and its configurable name (if any) must be checked for collisions the same way.
7. **Fact identity / discriminator.** Assumed unchanged in `knowledge`: ID is `Type U+001F Subject U+001F Discriminator` when `Fact.ID` is empty; `Add` rejects U+001F; `Spread` copies the discriminator as is. No change to `knowledge.Store`, `knowledge.Config` or `MergePolicy`. A stage that lets a merge policy see several facts (Stage 5 contradictions) will see discriminators set from data; nothing here prevents that.
8. **`knowledge.Store` / `Config`.** No change. No new tunables in any package; the only new configuration is the two names structs (R4.1).
9. **`infer`.** Not changed; data-set discriminators for inference products are explicitly deferred (Out of scope).

## Decisions

Recorded from the lead's review. Nothing in this section is open.

| # | Decision | Status |
| --- | --- | --- |
| D1 | `Typed` fails closed on a type mismatch and uses a single `H` for teller and listener (R1.2, R1.3). | Accepted |
| D2 | `Attributes.Sides` is required so side typos fail at load; keys are not validated (R2.1, R2.8). | Accepted |
| D3 | Numeric threshold conditions are out of scope until a consumer needs them. | Accepted |
| D4 | Discriminator sources are named functions on `Bindings`, not a reuse of `Attributes.Lookup` (R3.1). | Accepted |
| D5 | **Changed by the lead.** The library does not fix its vocabulary: the three condition names are fields of `AttributeNames` (`DefaultAttributeNames()`: `attr_equals`, `attr_in`, `attr_same`) passed to `RegisterAttributeOps`, and the new arg names are fields of `ArgNames` (constructor `DefaultArgNames()`, field `Bindings.ArgNames`, fields `Discriminator` and `DiscriminatorFrom`; Stage 5 adds `Roles` for both `add_fact` and `knows`). Zero values are valid via defaults (R2.2, R2.2a, R3.1a). | Replaces the earlier "fixed names" proposal |
| D6 | Inference products get no data-set discriminators in this stage. | Accepted |
| D7 | Stage 1 confirmed `Spreader.Transform` is `func(e Encounter, delivered knowledge.Fact) knowledge.Fact` with `Encounter` unchanged, so `TypedTransform` is promoted from Could to Should (R1.7). | Decided by lead |
| D8 | Stage 5 will add an optional `roles` arg to `add_fact` and `knows`. The `Unknown(...)` allow-list is one extendable list and the union rule is documented (R3.9). | Decided by lead |
| D9 | `TypedTransform` returns the delivered copy unchanged on a type mismatch (R1.8). | Accepted |
| D10 | `AttributeNames` is wiring and is not part of `declare.Config` / `Document` (R2.2b). | Accepted |
| D11 | Numeric attribute values compare by float64 value (R2.7). | Accepted |
| D12 | No new error sentinels (R4.4). | Accepted |
| D13 | The attribute-condition arg keys (`side`, `key`, `value`, `values`, `other_side`, `other_key`) stay fixed. | Accepted |
| D14 | **Rename driven by Stage 5.** The args-names type is `ArgNames` (not `AddFactArgNames`) with `DefaultArgNames()`; the `Bindings` field stays `ArgNames`. It serves both `add_fact` and `knows`, grows only by adding fields, and a zero field means its default. | Decided by lead |
