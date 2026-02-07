# Shelley FUSE Justfile

# Default: list available commands
list:
    just --list

# Begin work on a ticket
start-work ticket:
    tk start {{ticket}} && git commit -m "Start ticket {{ticket}}" .tickets/{{ticket}}.md
    git worktree list | grep -q {{ticket}} || git worktree add ../worktree/shelley-fuse/{{ticket}}

# Build the FUSE binary
build:
    go build -o shelley-fuse ./cmd/shelley-fuse

# Run all tests
test:
    go test ./...

# Run integration tests (requires /usr/local/bin/shelley and fusermount)
test-integration:
    go test -v ./fuse -timeout 60s

# Start shelley-fuse for manual testing (Ctrl+C to stop and unmount)
dev mount="/shelley" url="http://localhost:9999":
    just build
    mkdir -p {{mount}}
    ./shelley-fuse {{mount}} {{url}}

# Finish work on a ticket: close, rebase onto main, ff-merge, remove worktree+branch
# Idempotent — safe to run repeatedly. Agents should run until exit 0.
# Ticket is optional when run from a worktree (inferred from branch/directory).
finish-work *ticket:
    ./scripts/finish-work {{ticket}}

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
