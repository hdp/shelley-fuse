# Shelley FUSE Justfile

# Default: list available commands
list:
    just --list

# Begin work on a ticket: create worktree and launch implementing agent
start-work ticket:
    #!/usr/bin/env bash
    set -euo pipefail
    worktree_dir="/home/exedev/worktree/shelley-fuse/{{ticket}}"
    if git worktree list | grep -q "{{ticket}}"; then
        echo "ERROR: Worktree already exists for {{ticket}}." >&2
        echo "Use 'just implement {{ticket}}' to resume work or 'just review {{ticket}}' to review." >&2
        exit 1
    fi
    git worktree add "$worktree_dir"
    main_wt="$(git worktree list --porcelain | awk '/^worktree /{print $2; exit}')"
    "$main_wt/scripts/launch-agent" implement "{{ticket}}" "$worktree_dir"

# Print the next ticket ready for work (no matching worktree)
next-ticket:
    #!/usr/bin/env bash
    set -euo pipefail
    worktrees="$(git worktree list)"
    while IFS= read -r line; do
        ticket="$(echo "$line" | awk '{print $1}')"
        if ! echo "$worktrees" | grep -q "$ticket"; then
            echo "$ticket"
            exit 0
        fi
    done < <(tk ready | awk '{print $1}')
    echo "No tickets ready for work." >&2
    exit 1

# Launch an implementing agent on a ticket's worktree
implement ticket:
    #!/usr/bin/env bash
    set -euo pipefail
    main_wt="$(git worktree list --porcelain | awk '/^worktree /{print $2; exit}')"
    worktree_dir=$(git worktree list --porcelain | awk -v t="{{ticket}}" '/^worktree /{ wt=$2 } /^branch /{ if ($2 ~ t) print wt }')
    if [ -z "$worktree_dir" ]; then
        candidate="/home/exedev/worktree/shelley-fuse/{{ticket}}"
        if git worktree list | grep -q "$candidate"; then
            worktree_dir="$candidate"
        fi
    fi
    if [ -z "$worktree_dir" ]; then
        echo "ERROR: No worktree for {{ticket}}." >&2
        echo "Use 'just start-work {{ticket}}' to create one." >&2
        exit 1
    fi
    "$main_wt/scripts/launch-agent" implement "{{ticket}}" "$worktree_dir"

# Launch a review agent on a ticket's worktree
review ticket:
    #!/usr/bin/env bash
    set -euo pipefail
    main_wt="$(git worktree list --porcelain | awk '/^worktree /{print $2; exit}')"
    worktree_dir=$(git worktree list --porcelain | awk -v t="{{ticket}}" '/^worktree /{ wt=$2 } /^branch /{ if ($2 ~ t) print wt }')
    if [ -z "$worktree_dir" ]; then
        candidate="/home/exedev/worktree/shelley-fuse/{{ticket}}"
        if git worktree list | grep -q "$candidate"; then
            worktree_dir="$candidate"
        fi
    fi
    if [ -z "$worktree_dir" ]; then
        echo "ERROR: No worktree for {{ticket}}. Nothing to review." >&2
        exit 1
    fi
    "$main_wt/scripts/launch-agent" review "{{ticket}}" "$worktree_dir"

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

# Archive Shelley conversations for worktrees that have been cleaned up
archive-finished:
    #!/usr/bin/env bash
    set -euo pipefail
    find /shelley/conversation -maxdepth 2 -name ctl | while read ctl; do
        fgrep -qf <(git worktree list --porcelain | awk '/^worktree /{print "cwd="$2}') $ctl && continue
        touch $(dirname $ctl)/archived
    done

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
