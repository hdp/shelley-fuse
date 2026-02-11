# Shelley FUSE Justfile

# Default: list available commands
list:
    just --list

# Begin work on a ticket
start-work ticket:
    git worktree list | grep -q {{ticket}} || git worktree add ../worktree/shelley-fuse/{{ticket}}
    cd ../worktree/shelley-fuse/{{ticket}} && tk start {{ticket}}

# Build the FUSE binary
build binary="./shelley-fuse":
    go build -o {{binary}} ./cmd/shelley-fuse

# Run all tests
test:
    go test ./...

# Start shelley-fuse for manual testing (Ctrl+C to stop and unmount)
dev mount="~/mnt/shelley" url="http://localhost:9999":
    just build
    just run-dev {{mount}} {{url}}

run-dev mount="~/mnt/shelley" url="http://localhost:9999":
    @mkdir -p {{mount}}
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

# Install shelley-fuse: build, install binary, install systemd unit, enable and start
install:
    just build
    sudo install -m 755 shelley-fuse /usr/local/bin/shelley-fuse
    sudo cp shelley-fuse.service /etc/systemd/system/shelley-fuse.service
    sudo systemctl daemon-reload
    sudo systemctl enable shelley-fuse.service
    sudo systemctl restart shelley-fuse.service
    @echo "shelley-fuse installed and started"
    @echo "Mount point: /shelley"
    @echo "Check status: systemctl status shelley-fuse"

# Clean build artifacts
clean:
    rm -f shelley-fuse
