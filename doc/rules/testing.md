# Testing rules

Settled. Follow them; mirror the nearest existing `_test.go` for style.

1. Standard library `testing` only, unless the user approves a dependency.
2. Table-driven tests for pure logic. Hermetic: no network, no files outside `t.TempDir()`, no reliance on prior state.
3. Examples in tests and README use an invented neutral domain (e.g. a village notice-board or office gossip), never the originating game.
4. Every config default has a test asserting its value and that overriding it changes behaviour.
5. Concurrency-safe types are tested with `go test -race`.
6. Determinism: tests inject a seeded RNG and a fake clock/tick source.
7. The leak guard test (see `architecture.md`) must pass.
8. Entry points: `go test ./...`, `go test -race ./...`, `go vet ./...`. Add a `justfile` or Makefile with `test`, `test-race`, `lint` and use those names.
9. A phase is not done until tests pass with `-race` and `go vet` is clean.
