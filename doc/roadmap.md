# Roadmap

High-level plan for improving rumour_mill after the v0.1.0 candidate. It lists capabilities, not designs; each item gets its own spec (`doc/rules/specs.md`) before any code. Stages are ordered by value for effort, and later stages are only started when a real consumer needs them. The library stays v0.x until at least two real consumers have used it.

## Stage 1: richer rumour behaviour (done)

Goal: let rumours change in transit based on who is talking, to whom, and when, without adding more hooks.

Specs: [PRD](specs/prd-stage1-richer-rumour-behaviour.md). Amends [knowledge](specs/knowledge.md), [spreading-and-inference](specs/spreading-and-inference.md) and [config](specs/config.md).

- **Context-aware distortion.** The single optional hook on spreading should see the teller, the listener and the current tick, not only the fact. This covers teller-dependent distortion and age-based fading.
- **Time-based fading.** A caller-driven maintenance step on a knowledge store, run once per tick or day, so confidence can fall with age, old facts can be forgotten, and the library still has no clock.

## Stage 2: less glue code

Goal: a new game should need fewer lines of Go to wire up its data.

Specs: [PRD](specs/prd-stage2-less-glue-code.md). Amends [spreading-and-inference](specs/spreading-and-inference.md) and [declarative](specs/declarative.md).

- **Typed spreading filters.** A helper so filter authors write against their own types instead of asserting them by hand.
- **Generic attribute conditions for data files.** Optional built-in conditions over caller-supplied attributes (equals, one of, same value as the event), following the existing bindings pattern, so most relational rules need no custom Go.
- **Identity control from data.** Let data files set a fact's discriminator directly, so rules can decide when repeated claims merge and when they stay separate.

## Stage 3: documentation and patterns

Goal: cover needs that the current design already supports but does not explain.

Specs: [PRD](specs/prd-stage3-docs-and-patterns.md). Recipes land after Stages 1 and 2. Touches [declarative](specs/declarative.md).

- **Recipes.** README sections for claims involving several parties, group or faction beliefs (a group as just another holder), and per-occurrence versus merged facts.
- **Data-file hygiene.** Document the convention of testing that every data file loads, since name mistakes surface at load time.
- **Second real consumer.** Use the library in a project outside this repository and record what it needed. This gates the v0.1.0 tag and any 1.0 discussion.

## Stage 4: scale and persistence, only when measured

Goal: handle large simulations without changing the public API unless a consumer demands it.

Specs: [PRD](specs/prd-stage4-scale-and-persistence.md). Relates to [knowledge](specs/knowledge.md) and [spreading-and-inference](specs/spreading-and-inference.md).

- **Benchmarks.** Measure realistic store sizes and holder counts before optimising anything.
- **Internal indexing.** Keep fact lookups fast for large stores, with no API change.
- **Persistence seam.** Introduce a storage interface only when a real second backend or a consumer-side test seam exists (architecture rule 8).

## Stage 5: deeper semantics, only on demand

Goal: capabilities that need larger design work and have no consumer yet.

Specs: [PRD](specs/prd-stage5-deeper-semantics.md). Amends [knowledge](specs/knowledge.md), [spreading-and-inference](specs/spreading-and-inference.md) and [declarative](specs/declarative.md).

- **Contradictions.** Detecting and resolving facts that conflict, which needs the merge policy to see more than one fact.
- **Absence in inference.** Rules that fire on a fact being missing.
- **Richer claims.** First-class multi-party or relational facts, if recipes from stage 3 prove too awkward.

## Not planned

Event delivery, audience selection, a world model, schedulers, dialogue, combat and economy remain out of scope (see `doc/specs/overview.md`).
