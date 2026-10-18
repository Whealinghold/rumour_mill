# rumour_mill

A reusable, configurable Go library for NPC rumour propagation: layered event rules, per-entity knowledge with provenance and confidence, gossip spreading with decay, and fact inference. It was extracted (as a redesign, not a copy) from the Epic Whispers game. It knows nothing about any game or setting.

## Mandatory context (read before any task)

Read all of these at the start of a session, before analysis, search, or edits. Tags are not a substitute for reading the files.

| Tag | Path |
| --- | --- |
| `#architecture` | `doc/rules/architecture.md` |
| `#testing` | `doc/rules/testing.md` |
| `#specs` | `doc/rules/specs.md` |

If a file is unread, your only valid next step is to read it. Then read `doc/README.md` (the index) and any spec in `doc/specs/` relevant to the task.

## Layout

```
rumour_mill/
├── CLAUDE.md, AGENTS.md
├── go.mod
├── doc/                  ← singular "doc", never "docs"
│   ├── README.md         ← index; update it with every new doc
│   ├── rules/            ← settled rules for agents
│   ├── specs/            ← specifications; write the spec BEFORE the code
│   └── design/           ← design notes and reference material
└── (Go packages)
```

Module: `github.com/Whealinghold/rumour_mill` (Go). Licence: MIT.

## Core requirement

**Reusable and configurable. No hardcoded domain facts.** See `doc/rules/architecture.md`; it is the most important rule in this repo.

## What agents must not do

- Do not copy code or vocabulary from Epic Whispers. `doc/design/reference/` (if present) is read-only design reference; rewrite, never paste.
- Do not use `docs/`; the directory is `doc/`.
- Do not add dependencies without asking; standard library first.
- Do not widen scope beyond the current phase in the plan.
