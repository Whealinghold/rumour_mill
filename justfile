default: test

test:
    go test ./...
    cd contrib/prometheus && go test ./...
    cd contrib/otel && go test ./...

test-race:
    go test -race ./...
    cd contrib/prometheus && go test -race ./...
    cd contrib/otel && go test -race ./...

lint:
    go vet ./...
    cd contrib/prometheus && go vet ./...
    cd contrib/otel && go vet ./...

# Pin the adapter modules to a root release and print the tag commands.
# Usage: just release-adapters v0.2.0        (checks, pins, prints)
#        just release-adapters v0.2.0 dry    (changes nothing, runs nothing)
# Never tags or pushes; run the printed commands yourself.
release-adapters version dry="":
    #!/usr/bin/env bash
    set -euo pipefail
    case "{{version}}" in
      v[0-9]*.[0-9]*.[0-9]*) ;;
      *) echo "version must look like v1.2.3, got '{{version}}'" >&2; exit 1 ;;
    esac
    if [ -n "$(git status --porcelain)" ]; then
      echo "working tree is dirty: commit or stash first" >&2
      [ "{{dry}}" = "dry" ] || exit 1
      echo "(dry run: continuing)" >&2
    fi
    if [ "{{dry}}" != "dry" ]; then
      just test-race
      just lint
      # Both edits or neither: the tree is clean here, so a checkout restores the originals.
      trap 'git checkout -- contrib/prometheus/go.mod contrib/otel/go.mod' ERR
      for m in prometheus otel; do
        (cd contrib/$m && go mod edit -require=github.com/Whealinghold/rumour_mill@{{version}})
      done
      trap - ERR
      echo "pinned contrib/prometheus and contrib/otel to {{version}} (replace kept for in-repo dev)"
    else
      echo "(dry run: skipped tests, lint and go mod edit)"
    fi
    echo
    echo "Next, review the go.mod changes, commit them, then run:"
    echo "  git tag {{version}}"
    echo "  git tag contrib/prometheus/{{version}}"
    echo "  git tag contrib/otel/{{version}}"
    echo "  git push origin {{version}} contrib/prometheus/{{version}} contrib/otel/{{version}}"
