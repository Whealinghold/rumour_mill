# Reference material (read-only)

Design reference only: the source of the originating game's rule/knowledge engine, with a `.txt` suffix so it is not compiled. It is **not** a template. It contains hardcoded game vocabulary and constants that this library must NOT inherit. Read it to understand the ideas (layered rules, provenance-ranked facts, confidence decay when spreading, inference), then design the library from scratch per `doc/rules/architecture.md`.

The leak guard test must exclude this directory.
