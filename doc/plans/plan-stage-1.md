# Plan: Stage 1, richer rumour behaviour

**Status: Proposed.** Sequences [prd-stage1-richer-rumour-behaviour](../specs/prd-stage1-richer-rumour-behaviour.md) (roadmap Stage 1) into 5 phases. Built against `doc/rules/architecture.md` (no domain vocabulary, no magic numbers, abstract time, injected policy, "Idiomatic Go" section), `doc/rules/testing.md` (stdlib `testing`, table-driven, `go test -race`, `go vet`, defaults-tested-and-overridable) and `doc/rules/specs.md` (spec before code; specs updated in the same change as code). No `doc/plans/` format convention existed; this file uses the generic plan template. A plan decides nothing: every behavioural choice below is the PRD's (R-numbers cite it); the "Decisions" section lists only the small sequencing and implementation calls this plan makes itself.

Verified against the code at commit 56563d2: `spread/spread.go` (`Spreader.Transform func(knowledge.Fact) knowledge.Fact`, `delivered()` at line ~226), `knowledge/{store,fact,config}.go`, `declare/document.go` (strict decode, `Load`), `justfile` (`test`, `test-race`, `lint`).

## Scope

**In scope** (PRD U1-U4, all requirements R1-R28):
- U1 `spread.Transform` named type taking the `Encounter` plus the delivered copy.
- U2 `knowledge.Fact.FadedAt`, `knowledge.Fader`, `knowledge.FadeResult`, `knowledge.WithFader`, `(*Store).Fade`.
- U3 `knowledge.FadeConfig` at `Config.Fade`, built-in exponential half-life policy, validation, data-file loading through the existing `knowledge:` section.
- U4 spec, README, example and CHANGELOG updates.

**Out of scope** (PRD "Out of scope", unchanged; none of it is deferred work owned by this plan):
- Clock, scheduler or auto-fading; declarative transforms; `spread.Chain`; transform veto return; filter signature changes; origin-time on `Provenance`; fading in `infer`; per-type or non-exponential built-in curves.
- A `declare`-level cross-check that `fade.half_lives` keys are ranked source kinds (see Decision P-D11; the store accepts unranked kinds, so fade does too).
- Stage 2 `spread.TypedTransform`, Stage 4 indexing/benchmarks, Stage 5 conflicts and roles. Stage 1 only has to not obstruct them (see "Cross-stage assumptions").

## Gated work downstream (explicitly NOT scheduled by this plan)

| Gate | Not started until | This plan's only obligation |
| --- | --- | --- |
| Stage 3 recipes | Stage 1 and Stage 2 both landed (Stage 3 PRD D4) | Leave the final `Transform`/`Fade` API documented so recipes can compile against it |
| Stage 3 "second real consumer" task | Recorded real use outside this repo; gates the v0.1.0 tag | None. Not a code phase; do not tag v0.1.0 as part of Stage 1 |
| Stage 4 benchmarks, indexing, persistence seam | A measured need or a second backend/consumer test seam (architecture rule 8; Stage 4 PRD A1-A3) | `Fade` mutates the fact map in one place (Phase 3) so a later index can hook it; `FadedAt` round-trips through `All()` -> `Add` unchanged |
| Stage 5 contradictions, absence, roles | A real consumer needs them | `Fade` never calls into `Add` and leaves identity fields alone; `Transform` restore list is exactly the PRD's (no `Roles`) |

## Assumptions this plan makes (flagged, not decided)

- **P1.** The user's approval of the spec amendments (specs rule 1) is a hard gate between Phase 1 and Phase 2. If the user has already approved the PRD's text as the spec direction, Phase 1's spec edits are still reviewed before any Go code lands.
- **P2.** U1 and U2 are independent (different packages, no shared file) and could run in parallel, as the PRD suggests; this plan serialises them (U1 then U2) so each phase ends with a green `go test -race ./...`. Re-order freely.
- **P3.** `CHANGELOG.md` gets a new `## [Unreleased]` section above `[0.1.0]` rather than editing the unreleased 0.1.0 entry, because 0.1.0 is not tagged but already describes a complete slice and Stage 1 contains a breaking change that deserves its own heading.
- **P4.** `doc/roadmap.md` Stage 1 is marked done and the `doc/README.md` Roadmap-PRDs line updated in the final phase, mirroring "update the roadmap in the same change as the spec". (This planning task did not edit them.)

## Decisions (made by this plan; PRD decisions D1-D8 are honoured as written)

| # | Decision | One-line rationale |
| --- | --- | --- |
| P-D1 | `delivered()` takes the `Encounter` (already built for the filters) instead of `(teller, fact, now)`; `Spread` builds it once per (listener, fact) and passes the same value to `allowed` and `delivered`. | R1 says "same value the filters judged"; one construction guarantees it. |
| P-D2 | `delivered()` sets `out.FadedAt = 0` on the listener's copy. | The listener acquired the fact at `now`; inheriting the teller's `FadedAt` would be a stale settle point. This corrects the PRD's "spread copies need no change" (they copy `FadedAt` by struct assignment today); behaviour with a sane caller is unchanged, the reset is hygiene. |
| P-D3 | After `Transform`, still copy back only `Confidence`, `Payload`, `Expiry` from its result (the existing code shape). | That is already exactly R4's "restore" guarantee, and keeps any future `Fact` field (`FadedAt`, `Roles`) outside the transform's reach by default. Stage 5's PRD D6 wants `Roles` transformable: Stage 5 adds it to that copy list then. |
| P-D4 | `Fade` is a no-op returning an empty `FadeResult` when the store has no fader, even if `ForgetBelow > 0` (R17). | PRD is explicit; fading, including forgetting, is opt-in. Documented in the spec so it is not a surprise. |
| P-D5 | A per-source `HalfLives` entry of `0` means "this source never fades" even when `DefaultHalfLife > 0`; absence falls back to `DefaultHalfLife`. | R21 reads "a half-life of 0 ... means never fades"; this is the only reading that lets a map exempt one source from a non-zero default. |
| P-D6 | `elapsed` is `0` when `now <= settled`, and the subtraction saturates at `math.MaxInt64` instead of wrapping. | R9 "never negative"; ticks are caller-controlled `int64`, so overflow is a reachable bad input and the library must not misbehave on it (architecture rule 12). |
| P-D7 | `NewStore` clones `Config.Fade.HalfLives` (like `SourceRanks`) and builds the exponential fader after options run, only if `WithFader` did not set one. | Caller mutating its map later cannot change a live store; `WithFader` wins (R18). |
| P-D8 | `FadeConfig` and its validation live in `knowledge/config.go`; `Fader`, `FadeResult`, `WithFader`, `Fade`, the exponential builder live in a new `knowledge/fade.go`. No new package. | Smallest surface and file churn; `config.go` already owns `Validate`. |
| P-D9 | Default `HalfLives` is an empty non-nil map; `declare.Load` needs no special-casing (merging into an empty map equals replacing it). | Keeps `DefaultConfig()` fresh-copy rule trivial (R25) and avoids touching `declare/document.go`. |
| P-D10 | Float comparisons in tests use a `1e-9` tolerance, stated in a test constant. | PRD NFR (math.Pow platform stability). |
| P-D11 | No `declare`-level check that `fade.half_lives` keys appear in `source_ranks`. | `knowledge` accepts unranked source kinds (`UnrankedRank`); fade follows the same rule and `declare` needs no change. |

## Test levels (from `testing.md`; no pyramid doc beyond it)

Unit: table-driven, one package, injected tick values, no RNG needed (neither feature is random). Example: `Example_*` that doubles as docs. Integration: two or more packages (`declare.Load` + `knowledge` + `spread`). No e2e or UI level exists. Every phase ends with `just test`, `just test-race`, `just lint` clean plus `TestNoDomainVocabulary`, `TestExportedIdentifiersAreDocumented`, `TestImportBoundaries` passing (testing rule 9).

---

## Phase 1: Spec amendments and usage-first example sketches (docs only, user-approved)

**Why first:** specs rule 1 (spec before code, approved) and architecture "usage first". Every later phase codes to this text.

- Ordering: these Stage 1 spec edits to A2, A5 and A8 land before Stage 2's `Typed`/`TypedEncounter` spec edits to the same file; Stage 2 Phase 1 re-reads A2 and A8 first.
- Amend `doc/specs/spreading-and-inference.md`: A2 (new `Transform` type and field doc; `Encounter` doc stays), A4 step 4 (transform gets the encounter; position unchanged), A5 (nil/NaN unchanged, panics propagate), A6 (`Spread` and `Fade` not safe for concurrent use against a shared store), A7 row ("Confidence formula fixed" now `DecayFactor` + the context-aware `Transform`), A8 office example rewritten to the new signature plus a teller/listener/age example in the neutral harbour-market domain from the PRD.
- Amend `doc/specs/knowledge.md`: section 2 API (`Fact.FadedAt`, `Fader`, `FadeResult`, `WithFader`, `Fade`), Config block and table (`Fade FadeConfig`: `HalfLives`, `DefaultHalfLife`, `ForgetBelow` with defaults `empty / 0 / 0`; zero `Config{}` still valid), section 3 new behaviour "Fade" (settle point `max(Observed, FadedAt)`, composition, skip-expired, never prune/evict, forget rules, NaN rule, opt-in no-op incl. P-D4, `Private` faded per D8), section 4 errors (four new `ErrInvalidConfig` cases), section 5 concurrency/determinism (fader synchronous on caller goroutine, must not re-enter the store), section 6 mapping row, section 7 worked example.
- Amend `doc/specs/config.md`: add `knowledge.fade.half_lives`, `default_half_life`, `forget_below` to the defaults table (each "tested by" column filled) and to the YAML/JSON document example; note that `half_lives` merges into an empty default so merge and replace coincide.
- Amend `doc/specs/overview.md` extension-point table: reword the `Transform` row (no longer "the only hook of its kind"), add rows for `knowledge.Fader` via `knowledge.WithFader` and `knowledge.Config.Fade`; keep the out-of-scope line "no built-in distortion strategies".
- Record the user approval (P1) in the commit message. PRDs are not edited by this plan.

**Tests:** none (docs). Gate check instead: `TestNoDomainVocabulary` scans `.md` files, so run `just test` to prove the spec text has no banned terms; grep the amended specs for every R-number's behaviour (R1-R26 each appear once in specs).

**Observability & rollback:** docs only; revert the commit. No runtime blast radius.

**Acceptance:** user has approved the amended specs (P1). `just test` clean. Spec text and PRD agree on every signature in the PRD's "Cross-stage interfaces".

## Phase 2: U1 context-aware `Transform` (`spread`)

**Why first of the code phases:** smallest change, contains the only breaking API change, and its migration cost is visible in the existing tests immediately. Independent of Phase 3 (P2).

- `spread/spread.go`: add `type Transform func(e Encounter, delivered knowledge.Fact) knowledge.Fact` with a doc comment (read-only `Encounter` and `Fact.Prov.Premises`, R7; panics propagate; position in the pipeline, R3) and change the field to `Transform Transform` (R6). Extends the existing `Spreader`; no new type besides the named func type.
- Refactor per P-D1: `Spread` builds `e := Encounter{Teller, Listener, Fact: fact, Now: now}` once; `allowed(e)`; `delivered(e)`. `delivered` calls `s.Transform(e, out)` and copies back only `Confidence`, `Payload`, `Expiry` (P-D3, R4); NaN still drops, clamp unchanged (R3, R4).
- Migrate existing tests, which the PRD under-counts (it names only the spread example): `spread/spread_test.go` lines ~285 (`sp.Transform = func(f) ...`), `TestTransform` table (~336-399, field type `func(knowledge.Fact) knowledge.Fact`), `TestTransformSeesDecayedCopy` (~407-415); `spread/example_test.go` `Example_office` (~71); `spread/bench_test.go` if Stage 4 Phase 1 has landed (its `Transform` variant). Mechanically wrap: `func(_ spread.Encounter, f knowledge.Fact) knowledge.Fact`. Expected outputs must not change (R5, success measure 3).
- New tests in `spread/spread_test.go`, written first and table-driven, with the harbour-market neutral domain (S1-S5): teller-dependent (`e.Teller.ID()`), listener-dependent, age-dependent (`e.Now - e.Fact.Observed`, deliveries at ticks 40 and 90), `e.Fact` is the teller's pre-decay fact while `delivered` is decayed (R2), identity fields forced back and NaN dropped and 1.7 clamped to 1 (R4), nil transform unchanged and below-`MinConfidence` skipped (R3, R5), the `Encounter` handed to `Transform` equals the one the filters saw (R1; use a recording filter and transform), the teller's store byte-for-byte unchanged after `Spread` (R7).
- New `Example_transformContext` in `spread/example_test.go` showing all three use cases with the one hook (success measure 1).

**Reuse:** extends `Spreader`/`Spread`/`delivered`; reuses `helpers_test.go` (`person`, `newPerson`, `holders`) and the existing `TestTransform` table shape.

**Tests:** unit (S1-S5, R-traceability table below) and example. No integration needed: `declare` does not touch `Transform` (grep confirms no `declare` reference).

**Observability & rollback:** library has no logging; `Result.Transfers` is the surface and is unchanged. Rollback = revert; the migration is a compiler-flagged one-line wrapper (PRD rollout). No data state.

**Acceptance:** S1-S5 pass; all migrated tests pass with unchanged expected output; `just test-race`, `just lint` clean.

## Phase 3: U2 `FadedAt`, `Fader`, `Store.Fade` (`knowledge`)

**Why here:** U3 builds on it; the `FadedAt` reset in Phase 2's `delivered()` lands here as soon as the field exists.

- `knowledge/fact.go`: add `FadedAt Tick` to `Fact` with doc comment (zero = never faded; not part of identity; not validated by `Add`; `clone` needs no change since it is a value).
- `knowledge/fade.go` (new): `type Fader func(f Fact, now, elapsed Tick) (confidence float64, forget bool)`; `type FadeResult struct{ Changed, Forgotten []string }`; `func WithFader(f Fader) Option` (nil ignored, R18); `func (s *Store) Fade(now Tick) FadeResult`. `Store` gains an unexported `fader Fader`; `NewStore` leaves `fader` as set by options (the config-built policy is attached in Phase 4).
- `Fade` algorithm (R8-R16, P-D4, P-D6): if `s.fader == nil` return the zero result. Walk `slices.Sorted(maps.Keys(s.facts))` (same idiom as `Find`/`All`); skip `expired(f, now)` (R15); `settled := max(f.Observed, f.FadedAt)`; `elapsed` per P-D6; call fader with `clone(f)`; NaN -> leave fact unchanged and unsettled (R13); else clamp to `[0,1]`; if `forget` or `c < s.cfg.Fade.ForgetBelow` -> `delete(s.facts, id)` and append to `Forgotten` (R12); else write back `Confidence = c` and `FadedAt = max(f.FadedAt, now)` touching no other field (R14) and append to `Changed` only if `c != old` (R16). All map writes/deletes in `Fade` stay inside this one method (Stage 4/5 hand-off). If Stage 4's `put`/`drop` helpers exist, `Fade` must write and delete only through them (Stage 4 Phase 5 guard). Document concurrency and the no-re-entry rule (R19, R28).
- Phase 2 follow-up (one line): `spread.delivered` sets `out.FadedAt = 0` (P-D2) with a unit test: a teller whose fact has `FadedAt = 200` delivers a copy with `FadedAt == 0`.
- Tests, `knowledge/fade_test.go` (new), table-driven, with test faders (S6-S11): call order `a, b, c` and `Changed == [a b c]`; `Fade(100)` then `Fade(200)` sees `elapsed` 100 both times and `FadedAt == 200`, fact added at tick 150 sees 50 (R9-R11); expired fact untouched and still present until `Prune` (R15); `forget == true` and below-`ForgetBelow` removed and listed in `Forgotten` but not `Changed`, `Get` false (R12, R16); NaN leaves fact unchanged and unsettled, `1.4` stored as `1` (R13); no fader means empty result and unchanged store, `WithFader(nil)` ignored (R17, R18); only `Confidence` and `FadedAt` differ afterwards for every fact (R14); `Private` facts are passed to the fader and faded (D8); fader receives a copy: mutating it does not alter the store; `now` earlier than `settled` gives `elapsed == 0`; overflow-extreme ticks saturate and do not panic (P-D6); determinism: two identical runs on cloned stores (via `All()`/`Add`) give identical `FadeResult` and `All()`; `All()` -> `Add` round trip preserves `FadedAt` (Stage 4 hand-off); `Fade` on an empty store.
- `knowledge/example_test.go`: `ExampleStore_Fade` with a custom `Fader` (non-exponential curve, covering R26's "other curves via `WithFader`").

**Reuse:** `expired`, `clone`, the sorted-keys idiom, the `Option` pattern next to `WithMergePolicy`, `AddResult.Evicted` as the model for ID-list results.

**Tests:** unit and example; `-race` run (no concurrency claimed, so it only proves no data race in the test harness).

**Observability & rollback:** `FadeResult` is the observability surface (consumer logs `Changed`/`Forgotten`). Rollback = revert; no persisted format change beyond the new zero-valued field, which is invisible to older consumers' stores.

**Acceptance:** S6-S11 and the determinism and round-trip tests pass; Phase 2's `FadedAt` reset test passes; `just test`, `just test-race`, `just lint` clean.

## Phase 4: U3 `FadeConfig`, validation and the built-in exponential policy

**Why here:** needs `Fader`/`Fade` (Phase 3); the whole phase is opt-in and default-off.

- `knowledge/config.go`: `type FadeConfig struct { HalfLives map[string]int64 \`json:"half_lives" yaml:"half_lives"\`; DefaultHalfLife int64 \`json:"default_half_life" yaml:"default_half_life"\`; ForgetBelow float64 \`json:"forget_below" yaml:"forget_below"\` }` (field docs state units, defaults and the P-D5 reading); `Config.Fade FadeConfig \`json:"fade" yaml:"fade"\``; `DefaultConfig()` returns `Fade{HalfLives: map[string]int64{}}` (fresh each call, R22, R25, P-D9); `Validate` adds four joined `ErrInvalidConfig` checks: negative half-life, negative `DefaultHalfLife`, empty `HalfLives` key, `ForgetBelow` outside `[0,1]` or NaN (R24). Zero `Config{}` stays valid.
- `knowledge/fade.go`: unexported `exponentialFader(cfg FadeConfig) Fader` returning nil when `HalfLives` has no positive entry and `DefaultHalfLife == 0` (so R17 holds: no fader, `Fade` is a no-op). Curve: `confidence * math.Pow(0.5, float64(elapsed)/float64(h))`, `h` per P-D5; never fades when `h == 0` (R21). `NewStore`: clone `HalfLives` (P-D7), apply options, then `if s.fader == nil { s.fader = exponentialFader(s.cfg.Fade) }` (R18).
- Tests (S12, S13), `knowledge/config_test.go` and `knowledge/fade_test.go`: the PRD's harbour numbers (`overheard` 0.8, half-life 100, `ForgetBelow 0.15`: 0.4 at 100, 0.2 at 200, forgotten at 300 since 0.1 < 0.15; `witnessed` unchanged) within `1e-9` (P-D10); composition: `Fade(100)` then `Fade(200)` equals one `Fade(200)` on a clone (R11); `HalfLives` entry `0` exempts a source under a non-zero `DefaultHalfLife` and `DefaultHalfLife` applies to unlisted sources (P-D5); `Validate` table, one row per R24 case plus a multi-error row proving errors are joined and `errors.Is(err, ErrInvalidConfig)`; `DefaultConfig()` values match R22, two calls return distinct `HalfLives` maps, mutating the caller's map after `NewStore` does not change fading (P-D7); testing rule 4: each default is asserted and an override changes `Fade` behaviour; `WithFader` overrides the config-built policy; `ForgetBelow` alone with no fader changes nothing (P-D4); extreme `h` and huge `elapsed` give a finite confidence in `[0,1]`.

**Reuse:** extends `Config`/`DefaultConfig`/`Validate` and the `maps.Clone` pattern already used for `SourceRanks`; no new package or dependency (`math` only).

**Tests:** unit plus the testing-rule-4 default/override pairs.

**Observability & rollback:** as Phase 3. Rollback = revert; a data file already using `knowledge.fade` would then fail strict decode loudly (PRD rollout), not silently.

**Acceptance:** S12, S13 pass; all gates green.

## Phase 5: U4 data-file integration, README, examples, CHANGELOG, doc index

**Why last:** examples and docs are finalised against the final API (PRD "Suggested order").

- Integration test in `declare` (S14): extend `declare/document_test.go` (or a new `declare/fade_test.go`) loading a JSON document with `knowledge.fade` via `declare.Load`, building the store with `knowledge.NewStore(doc.Config.Knowledge)`, asserting the S12 numbers; a document with the typo key `knowledge.fade.half_live` fails (existing `DisallowUnknownFields`); a document with no `fade` loads with `Fade` equal to defaults. No change to `declare` code (R23). If an existing `declare` fixture asserts the entire loaded `Config` against `DefaultConfig()`, confirm it still passes with the new field (P-D9 expects yes).
- `README.md`: Quick start gains one `Transform` snippet (teller/listener/age, neutral domain) and one `Fade` call; keep it compile-checked by mirroring it in an `Example_` (spread and knowledge examples from Phases 2-3 are the compiled twin). Add a "Fading" note to the Spreading section.
- `CHANGELOG.md`: new `## [Unreleased]` (P3): Changed (breaking) `spread.Spreader.Transform` type with the one-line migration wrapper; Added `Fact.FadedAt`, `Store.Fade`, `Fader`, `FadeResult`, `WithFader`, `Config.Fade`; note for hand-written serialisers to add `FadedAt` (PRD rollout).
- `doc/README.md`: change the Roadmap-PRDs stage-1 line to "implemented" and note the specs amended (P4); `doc/roadmap.md` mark Stage 1 done (P4). The specs were already updated in Phase 1; this phase re-reads them against the shipped code and corrects any drift in the same change (specs rule 5).
- Verify every exported new identifier has a doc comment stating concurrency where relevant (R28): `Transform`, `Fader`, `FadeResult`, `WithFader`, `Store.Fade`, `FadeConfig`.

**Tests:** integration (S14) and the example/README parity check; full gate suite below. Requirement traceability: add to the PR description the R1-R28 -> test name table (below) with every row filled.

**Observability & rollback:** docs and one test; revert the commit. 

**Acceptance (whole stage):** every PRD requirement has a passing test or example; `just test`, `just test-race`, `just lint` clean; `TestNoDomainVocabulary`, `TestExportedIdentifiersAreDocumented`, `TestImportBoundaries` pass; the five doc files and CHANGELOG updated; behaviour for a consumer using no `Transform` and no `fade` config is unchanged (existing tests pass with only the mechanical `Transform` wrapper change).

---

## Requirement traceability (filled during execution)

| Requirements | Phase | Test(s) |
| --- | --- | --- |
| R1-R7 | 2 | S1-S5 tests in `spread/spread_test.go`, `Example_transformContext` |
| R8-R19 | 3 | `knowledge/fade_test.go` (S6-S11, determinism, round trip) |
| R20-R26 | 4 | `knowledge/config_test.go`, `knowledge/fade_test.go` (S12, S13) |
| R23, R27, R28 | 5 | `declare` S14 test, docs/guard tests, README/spec review |

## Security review

The library has no network, file or privilege surface; the "trust boundary" is the consumer's own code and data files.
- **Data file input (new key path `knowledge.fade.*`).** Worst case is a hostile or mistyped document. Unknown keys fail loudly (strict decode); negative, NaN and out-of-range values are rejected by `Validate` (R24); huge half-lives are legal and merely make fading negligible; tiny positive half-lives with huge `elapsed` yield `math.Pow` underflow to 0, which is handled (finite result in `[0,1]`, test in Phase 4). No data-file value is ever executed.
- **Resource exhaustion.** `Fade` is O(n log n) in store size per call and allocates the sorted key slice plus two ID lists, the same order as `Find`/`All`. A caller who calls it in a hot loop on a huge store only hurts itself; stores are documented as NPC-sized (Stage 4 owns scale).
- **Integer overflow.** Caller-controlled `int64` ticks: `now - settled` saturates (P-D6); `FadedAt = max(...)` has no arithmetic.
- **Caller code.** `Transform`/`Fader` panics propagate (consistent with filters, PRD NFR); a `Fader` that re-enters the same store is documented as forbidden (R19); a `Transform` mutating `Encounter.Fact.Prov.Premises` could corrupt later deliveries in the same `Spread` call because the slice is shared across listeners. The PRD accepts "documented, not enforced" (R7); this plan keeps that and notes the residual risk here rather than silently copying per delivery.
- **Forgery / identity.** A transform still cannot change `ID`, `Type`, `Subject`, `Discriminator`, `Source`, `Prov`, `Observed`, `Private` or `FadedAt` of the delivered copy (P-D2, P-D3). `Fade` cannot change identity or provenance (R14, test).
- **Secrets / PII / cross-tenant.** None handled; payloads are opaque.

## Compatibility & rollout

- **Breaking:** `Spreader.Transform` changes type. Library is `v0.x`; one exported field, compiler flags every call site; the one-line migration wrapper is in the CHANGELOG. No expand-contract is offered because carrying two hooks contradicts PRD D1 (single hook).
- **Additive, default-off:** `Fact.FadedAt` (zero value), `Config.Fade` (zero/empty), `Store.Fade` with no fader are no-ops; data files without `fade:` load unchanged; zero `knowledge.Config{}` stays valid.
- **Serialisers:** consumers persisting by copying `Fact` keep `FadedAt` automatically; hand-written serialisers must add it (CHANGELOG note). Dropping it only makes the next `Fade` decay from `Observed`, never a crash.
- **Rollback:** revert the change set; no data migration. A data file with `knowledge.fade` would be rejected by the reverted strict decoder, loudly.
- **Dependency failure during rollout:** none (standard library only, no new dependency; `go.mod` unchanged).
- **Not touched:** `declare` code, `rules`, `infer`, `logic`, filter signatures, `Encounter`, merge policy, fact identity/discriminator derivation, `MaxFacts` eviction ordering (note: eviction compares `Confidence`, so a faded store evicts differently from an unfaded one; this is intended and spec'd in Phase 1).

## Cross-stage assumptions this plan makes

- `spread.Transform` is exactly `func(e Encounter, delivered knowledge.Fact) knowledge.Fact`; `spread.Encounter` is `{Teller, Listener Holder; Fact knowledge.Fact; Now knowledge.Tick}`, unchanged. Stage 2's `spread.TypedTransform` and `spread.TypedEncounter[H]` are assumed to wrap this and are not built here.
- `knowledge.Fact.FadedAt Tick`; `knowledge.Fader`; `knowledge.FadeResult{Changed, Forgotten []string}`; `knowledge.WithFader`; `(*Store).Fade(now Tick) FadeResult`; `knowledge.FadeConfig` at `knowledge.Config.Fade`. Stage 3 recipes and Stage 4/5 PRDs reference these exact names.
- Stage 4 assumes `Fade` deletions and confidence writes are Store-internal and visible to an index (single mutation site in `Fade`), and that `All()` -> `Add` round-trips `FadedAt`; Stage 5 assumes `Fade` neither calls `Add` nor the conflict policy and leaves `FadedAt` alone on conflict changes. Neither adds code in Stage 1.
- Stage 2/5 allow-list and `Bindings` changes in `declare` do not interact with `knowledge.fade` decoding.
