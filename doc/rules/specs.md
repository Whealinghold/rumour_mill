# Spec rules

1. **Spec before code.** Write `doc/specs/<name>.md` and have it approved by the user before implementing it.
2. Specs live in `doc/specs/` (never `docs/`). Add each to `doc/README.md`.
3. Each spec states: purpose, public API (types and signatures), configuration fields with defaults, behaviour, error cases, concurrency guarantees, and worked examples in a neutral domain.
4. Mark anything that was a hardcoded constant in the source material as a config field, and say what its default is.
5. Specs are updated in the same change as the code they describe.
6. Design notes and reference extracts go in `doc/design/`; settled agent rules go in `doc/rules/`.
