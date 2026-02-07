# Shelley FUSE Justfile

# Default: list available commands
list:
    just --list

# Begin work on a ticket
start-work ticket:
    tk start {{ticket}} && git commit -m "Start ticket {{ticket}}" .tickets/{{ticket}}.md
    git worktree list | grep -q {{ticket}} || git worktree add ../worktree/shelley-fuse/{{ticket}}

# Build the FUSE binary
build binary="./shelley-fuse":
    go build -o {{binary}} ./cmd/shelley-fuse

# Run all tests
test:
    go test ./...

# Run integration tests (requires /usr/local/bin/shelley and fusermount)
test-integration:
    go test -v ./fuse -timeout 60s

# Start shelley-fuse for manual testing (Ctrl+C to stop and unmount)
dev mount="/shelley" url="http://localhost:9999":
    just build
    just run-dev {{mount}} {{url}}

run-dev mount="/shelley" url="http://localhost:9999":
    mkdir -p {{mount}}
    ./shelley-fuse {{mount}} {{url}}

# Start shelley-fuse for manual testing with autoreload
dev-reload:
    bash scripts/dev-reload 

# Finish work: close ticket, rebase onto main, ff-merge, remove worktree+branch.
# Idempotent — safe to run repeatedly until exit 0.
# From a worktree: ticket is inferred if omitted. Always runs main's copy of the script.
finish-work *ticket:
    "$(git worktree list --porcelain | awk '/^worktree /{print $2; exit}')/scripts/finish-work" {{ticket}}

# Clean up all worktrees and branches for tickets that are already closed
clean-finished:
    #!/usr/bin/env bash
    set -euo pipefail
    for branch in $(git branch --list | sed 's/^[* ] //'); do
        [ "$branch" = "main" ] && continue
        status=$(tk show "$branch" 2>/dev/null | awk '/^status:/{print $2}') || true
        if [ "$status" = "closed" ]; then
            echo "=== Cleaning up $branch ==="
            just finish-work "$branch"
        else
            echo "Skipping $branch (status: ${status:-unknown})"
        fi
    done

# Clean build artifacts
clean:
    rm -f shelley-fuse
