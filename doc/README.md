# rumour_mill documentation

## Rules (mandatory)
- [architecture](rules/architecture.md): reusability, configurability, no hardcoded facts; "Idiomatic Go" section
- [testing](rules/testing.md): test strategy and commands
- [specs](rules/specs.md): spec-first workflow and format

## Specs
Phase 1 specs approved with changes (see "Review changes" below). All packages are implemented (Phases 2-5); see [CHANGELOG](../CHANGELOG.md).
- [overview](specs/overview.md): scope, glossary, package layout, extension points, out of scope
- [knowledge](specs/knowledge.md): facts, provenance, source ranking, confidence, expiry, merge, queries (**implemented**)
- [rules](specs/rules.md): `logic` combinators, layered `rules` registry and pipeline (**implemented**)
- [spreading-and-inference](specs/spreading-and-inference.md): gossip spreading with decay and injected policy; inference engine (**implemented**)
- [declarative](specs/declarative.md): `declare` package: Catalog, named conditions/actions, rule loading (**implemented**)
- [config](specs/config.md): every tunable and default, aggregate config, YAML/JSON document format (**implemented**)

### Review changes (after Phase 4)
- `LoadRules` is documented as resolve-all-then-register-with-rollback (not a single atomic step). `declare.RulesConfig` embeds `rules.Config`. A nil `Decoder` means JSON. `Config.Validate` is the home of the two cross-section source-kind checks.
- Added: `ErrBadBindings`, `architecture_test.go` (import boundaries) and `docs_test.go` (every exported identifier documented).
- Generality proof: the hospital-ward domain (`declare/testdata/ward.json` + `declare/ward_test.go`) needed no library change.

### Review changes (after Phase 3)
- Zero values: `rules.Config{}` (empty `on_action_error` = continue) and `knowledge.Config{}` are valid. `spread.Config{}` and `infer.Config{}` are rejected: their zeros have no sensible meaning, and `skip_back_to_source` defaults to true.
- Spec clarified to match the code: `Spread` skips nil listeners, the teller and store-less holders; a NaN from `Transform` drops the delivery; `Run` computes each pass from the store as it stood at the pass start, then adds the results.

### Review changes (after Phase 2)
- `rules.Config.DefaultPriority` moved to `declare.RulesConfig`: it only affects loading, so `rules` could not honestly test it. `rules.Config` is just `on_action_error`.
- Rule 8 of `architecture.md` reworded: storage is a concrete in-memory type; an interface arrives with a real second backend or test seam.
- `knowledge`: `Add` rejects U+001F in type/subject/discriminator (`ErrInvalidFact`); `Get`/`All` are the raw view and `Find` the filtered view; the query floor is a query-level view only.

### Review changes (after Phase 1)
- Cut: `share_chance` (and the RNG with it), `max_facts_per_encounter`, the `scaled` inference mode, the `Decayer` hook, `KeyFunc`, store `Has`/`Remove`/`Len`/`Rank`, the `Backend` interface (deferred until a second backend exists), the `core` package (`Tick` lives in `knowledge`).
- Added: one optional `Spreader.Transform` func field for rumour distortion.
- Kept, each exercised by an example: `max_facts`, `max_hops`, `skip_back_to_source`, `max_passes`.
- Restructured: the declarative loader (Catalog, Args, Node, document) is its own `declare` package; spread and infer are concrete (no type parameters); only the caller context `C` is generic.
- Concurrency: `Store`, `Spreader`, `Engine` are not safe for concurrent use; `Registry` and `Catalog` lock.

- [roadmap](roadmap.md): staged plan for improving the library after v0.1.0

### Roadmap PRDs (drafted, not yet approved or implemented)
One PRD per roadmap stage. Stage 3's recipes should land after Stages 1 and 2; Stages 4 and 5 are gated on measured or real consumer need.
- [stage 1](specs/prd-stage1-richer-rumour-behaviour.md): context-aware `Transform`, time-based fading (`Store.Fade`, `Fader`, `Fact.FadedAt`)
- [stage 2](specs/prd-stage2-less-glue-code.md): `spread.Typed`/`TypedTransform`, attribute conditions, discriminator from data, `declare.ArgNames`
- [stage 3](specs/prd-stage3-docs-and-patterns.md): recipes, data-file hygiene, second-consumer task (gated)
- [stage 4](specs/prd-stage4-scale-and-persistence.md): benchmarks, internal indexing, round-trip contract
- [stage 5](specs/prd-stage5-deeper-semantics.md): absence in inference, conflict policy, roles (gated)

## Design
- [reference](design/reference/README.md): read-only source material from the originating game; inspiration only
