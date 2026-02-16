# Agent Review Workflow Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add an implement/review/finish cycle with review gating before merge, replacing the current start-work/finish-work two-step.

**Architecture:** Justfile recipes delegate to `scripts/launch-agent`, which selects a prompt from `scripts/prompts/` and invokes a configurable agent command. Agents chain to each other via `just review` / `just implement`. Ticket status simplifies to open/closed; worktree existence signals "in progress."

**Tech Stack:** Bash scripts, Justfile recipes, Markdown prompt templates

**Design doc:** `docs/plans/2026-02-16-agent-review-workflow-design.md`

---

### Task 1: Create `scripts/prompts/implement.md`

**Files:**
- Create: `scripts/prompts/implement.md`

**Step 1: Write the implement prompt**

This is the prompt given to an implementing agent. It must be self-contained — the agent has no other context. The `TICKET_ID` placeholder will be substituted by `launch-agent`.

```markdown
You are implementing ticket TICKET_ID.

## Your Inputs

1. **The ticket**: Read `.tickets/TICKET_ID.md` for requirements, design, and acceptance criteria. This is your specification — implement what it says.
2. **The commit history**: Run `git log main..HEAD --oneline` to see prior work on this branch. If there are previous commits, a reviewer sent this back for changes — the ticket itself will reflect what still needs to be done.
3. **AGENTS.md**: Read `AGENTS.md` for project conventions, build/test commands, and architecture.

## Your Task

- Implement the requirements from the ticket
- Run `just test` and ensure tests pass
- Commit your work with clear commit messages

## If the Ticket Is Too Large

If you cannot fully implement the ticket in one pass:

1. Implement as much as you can and commit it
2. Create new ticket(s) for the remaining work using `tk create`
3. If the current ticket has dependents (other tickets that depend on it), update the dependency chain: add the new ticket(s) as dependencies of those dependents using `tk dep`, so the new tickets take this ticket's place in the chain
4. Commit the new ticket files (`.tickets/*.md`)

## When You Are Done

Run this command as your **final action**:

```
just review TICKET_ID
```

This launches a review agent to check your work.

## Rules

- Do NOT run `just finish-work` — only a reviewer may merge
- Do NOT add "notes to the reviewer" in commits or the ticket — the code and ticket speak for themselves
- Do NOT modify the ticket's requirements, design, or acceptance criteria — if you think they're wrong, implement what you can and let the reviewer sort it out
```

**Step 2: Verify the file exists and reads correctly**

Run: `cat scripts/prompts/implement.md | head -5`
Expected: First lines of the prompt file

**Step 3: Commit**

```bash
git add scripts/prompts/implement.md
git commit -m "Add implement agent prompt template"
```

---

### Task 2: Create `scripts/prompts/review.md`

**Files:**
- Create: `scripts/prompts/review.md`

**Step 1: Write the review prompt**

```markdown
You are reviewing ticket TICKET_ID.

## Your Inputs

1. **The ticket**: Read `.tickets/TICKET_ID.md` for requirements, design, and acceptance criteria. This is the specification — the code should implement what it says.
2. **The diff**: Run `git diff main...HEAD` to see all changes on this branch.
3. **The commit history**: Run `git log main..HEAD` for context on what was done and in what order.
4. **AGENTS.md**: Read `AGENTS.md` for project conventions and architecture.

## Your Task

Review the implementation against the ticket's requirements and acceptance criteria.

1. **Read the ticket first.** Understand what was supposed to be built.
2. **Review the diff against main.** Does the code implement what the ticket asks for? Is it correct, clean, and consistent with project conventions?
3. **Run the tests**: `just test`. Do they pass?
4. **Check for ticket splits.** If new `.tickets/*.md` files were created, verify they correctly capture remaining work and that the dependency chain is maintained.
5. **Check for unintended changes.** Are there modifications outside the scope of the ticket?

## Your Decision

### To Approve

If the implementation satisfies the ticket's requirements and acceptance criteria:

```
just finish-work TICKET_ID
```

This closes the ticket, rebases onto main, merges, and cleans up the worktree.

### To Reject

If the implementation needs changes:

1. **Edit the ticket** to clarify requirements, fix confusing wording, or add notes about what's missing. Use the commit message for meta-observations (e.g. "tests pass but only due to X" or "acceptance criteria were ambiguous about Y"). The ticket should read as a clear spec for the next implementer — not a conversation log.
2. **Commit your ticket edits.**
3. Run this command as your **final action**:

```
just implement TICKET_ID
```

This launches a new implementing agent to continue the work.

## Rules

- Base your review on the **ticket and the code** — not on any narrative from the implementing agent
- The ticket is the source of truth for what *should* exist; the code is what *does* exist; your job is to compare them
- Do NOT implement fixes yourself — reject and let the implementing agent do it
- Do NOT weaken the ticket's requirements to match the implementation — if the code doesn't meet the spec, reject
```

**Step 2: Verify the file exists and reads correctly**

Run: `cat scripts/prompts/review.md | head -5`
Expected: First lines of the prompt file

**Step 3: Commit**

```bash
git add scripts/prompts/review.md
git commit -m "Add review agent prompt template"
```

---

### Task 3: Create `scripts/launch-agent`

**Files:**
- Create: `scripts/launch-agent`

**Step 1: Write the launch-agent script**

```bash
#!/usr/bin/env bash
#
# launch-agent <role> <ticket> <worktree-dir>
#
# Launches an agent with the appropriate prompt in the given worktree.
# role: "implement" or "review"
#
# Override the agent command by setting LAUNCH_AGENT_CMD to a command
# that accepts three arguments: <role> <ticket> <worktree-dir>
#
set -euo pipefail

role="${1:?Usage: launch-agent <role> <ticket> <worktree-dir>}"
ticket="${2:?Usage: launch-agent <role> <ticket> <worktree-dir>}"
worktree_dir="${3:?Usage: launch-agent <role> <ticket> <worktree-dir>}"

# Resolve the prompt template relative to the main worktree
main_wt="$(git worktree list --porcelain | awk '/^worktree /{print $2; exit}')"
prompt_file="$main_wt/scripts/prompts/${role}.md"

if [ ! -f "$prompt_file" ]; then
    echo "ERROR: Unknown role '$role'. Expected 'implement' or 'review'." >&2
    echo "No prompt template found at: $prompt_file" >&2
    exit 1
fi

# Substitute the ticket ID into the prompt
prompt="$(sed "s/TICKET_ID/$ticket/g" "$prompt_file")"

if [ -n "${LAUNCH_AGENT_CMD:-}" ]; then
    exec $LAUNCH_AGENT_CMD "$role" "$ticket" "$worktree_dir"
fi

# Default: launch claude with the prompt in the worktree
cd "$worktree_dir"
exec claude -p "$prompt"
```

**Step 2: Make it executable**

Run: `chmod +x scripts/launch-agent`

**Step 3: Verify it errors cleanly with bad input**

Run: `scripts/launch-agent badrole fake-ticket /tmp 2>&1; echo "exit: $?"`
Expected: Error message about unknown role, exit code 1

**Step 4: Commit**

```bash
git add scripts/launch-agent
git commit -m "Add launch-agent script with configurable agent command"
```

---

### Task 4: Add `implement` and `review` recipes to justfile

**Files:**
- Modify: `justfile`

**Step 1: Add the `implement` recipe**

Add after the `start-work` recipe:

```just
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
```

**Step 2: Add the `review` recipe**

Add after the `implement` recipe:

```just
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
```

**Step 3: Verify recipes appear in `just --list`**

Run: `just --list`
Expected: `implement` and `review` appear in the list

**Step 4: Verify error message with nonexistent ticket**

Run: `just implement fake-ticket 2>&1; echo "exit: $?"`
Expected: "ERROR: No worktree for fake-ticket." message, exit 1

**Step 5: Commit**

```bash
git add justfile
git commit -m "Add implement and review recipes to justfile"
```

---

### Task 5: Modify `start-work` and add `next-ticket`

**Files:**
- Modify: `justfile`

**Step 1: Update `start-work` to launch implement instead of `tk start`**

Replace the current `start-work` recipe with:

```just
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
```

**Step 2: Add `next-ticket` recipe**

```just
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
```

**Step 3: Verify `next-ticket` prints a ticket**

Run: `just next-ticket`
Expected: Prints a ticket ID (e.g. `sf-uh2s`) since there are open tickets with no worktrees

**Step 4: Commit**

```bash
git add justfile
git commit -m "Update start-work to use implement, add next-ticket recipe"
```

---

### Task 6: Update AGENTS.md

**Files:**
- Modify: `AGENTS.md`

**Step 1: Update the workflow section**

Replace the "Finishing Work" section with a new "Development Workflow" section:

```markdown
## Development Workflow

This project uses a review-gated workflow. Implementing agents do not merge their own work — a separate review agent checks the work first.

### As an Implementing Agent

You were launched by `just start-work` or `just implement`. Your job:

1. Read your ticket (`.tickets/{ticket}.md`) for requirements
2. Read `AGENTS.md` (this file) for project conventions
3. Implement the work, run `just test`, commit
4. When done, run `just review {ticket}` as your **final action**

Do NOT run `just finish-work` — that's the reviewer's job.

### As a Review Agent

You were launched by `just review`. Your job:

1. Read the ticket for requirements and acceptance criteria
2. Review `git diff main...HEAD` against the ticket
3. Run `just test`
4. Either:
   - **Approve**: run `just finish-work {ticket}` (closes ticket, rebases, merges, cleans up)
   - **Reject**: edit the ticket to clarify what's needed, commit the edit, then run `just implement {ticket}`

### Ticket Splitting

If a ticket is too large for one pass, create new tickets for remaining work with `tk create`, adjust the dependency chain with `tk dep`, and commit the new ticket files. The reviewer will check the split.

### Available Commands

- `just start-work {ticket}` — Create worktree and start implementing (first time)
- `just implement {ticket}` — Resume implementing in existing worktree
- `just review {ticket}` — Launch review on existing worktree
- `just finish-work {ticket}` — Approve: close, rebase, merge, clean up (reviewer only)
- `just next-ticket` — Print the next ticket ready for work
- `just test` — Run all tests
- `just build` — Build the binary
```

**Step 2: Verify AGENTS.md reads correctly**

Run: `head -40 AGENTS.md`
Expected: Updated content with new workflow section

**Step 3: Commit**

```bash
git add AGENTS.md
git commit -m "Update AGENTS.md with review-gated workflow instructions"
```

---

### Task 7: End-to-end smoke test

**Files:** (none — verification only)

**Step 1: Verify `just --list` shows all new recipes**

Run: `just --list`
Expected: `implement`, `review`, `next-ticket`, `start-work`, `finish-work` all present

**Step 2: Verify `just next-ticket` works**

Run: `just next-ticket`
Expected: Prints a ticket ID

**Step 3: Verify `just implement` error message without worktree**

Run: `just implement sf-uh2s 2>&1`
Expected: "ERROR: No worktree for sf-uh2s. Use 'just start-work sf-uh2s' to create one."

**Step 4: Verify `just review` error message without worktree**

Run: `just review sf-uh2s 2>&1`
Expected: "ERROR: No worktree for sf-uh2s. Nothing to review."

**Step 5: Verify `scripts/launch-agent` error with bad role**

Run: `scripts/launch-agent badrole sf-uh2s /tmp 2>&1`
Expected: "ERROR: Unknown role 'badrole'..."

**Step 6: Verify `start-work` error when worktree exists**

Create and remove a test worktree:
```bash
git worktree add /home/exedev/worktree/shelley-fuse/test-ticket
just start-work test-ticket 2>&1  # Should error: worktree exists
git worktree remove /home/exedev/worktree/shelley-fuse/test-ticket
```

**Step 7: Final commit if any adjustments were needed**

If smoke tests revealed issues that required fixes, commit those fixes.
