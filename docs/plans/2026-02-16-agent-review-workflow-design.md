# Agent Review Workflow Design

## Problem

The current workflow (`just start-work` / `just finish-work`) has no review step. An implementing agent works on a ticket and merges its own work. We want a review loop where a separate agent reviews before merging, and can reject work back to another implementing agent.

Additionally, the `in_progress` ticket status is fragile — agents frequently forget to commit status changes, and the real "in progress" signal is already the existence of a worktree.

## Status Model

Drop `in_progress` from the workflow. Derive state from ticket status + worktree existence:

| Ticket | Worktree? | Meaning |
|--------|-----------|---------|
| open   | no        | Available for work |
| open   | yes       | Being implemented or awaiting review |
| closed | no        | Done (merged into main) |
| closed | yes       | Stale — `clean-finished` cleans up |

## Justfile Recipes

### Modified

- **`start-work {ticket}`** — Creates worktree if needed, then calls `launch-agent implement`. No longer calls `tk start`.
- **`finish-work {ticket}`** — Unchanged in purpose. Closes ticket, rebases, merges, removes worktree. Already idempotent.

### New

- **`implement {ticket}`** — Launches an implementing agent in the ticket's existing worktree. Errors if no worktree exists (directs user to `start-work`).
- **`review {ticket}`** — Launches a review agent in the ticket's existing worktree. Errors if no worktree exists.
- **`next-ticket`** — Prints the ID of the first `tk ready` result with no matching worktree.

### Relationship

`start-work` = create worktree + `implement`. After that, `implement` and `review` alternate until `finish-work` merges.

## `scripts/launch-agent`

```
launch-agent <role> <ticket> <worktree-dir>
```

- `role` is `implement` or `review`
- Selects a prompt from `scripts/prompts/{role}.md` and launches an agent in `worktree-dir`
- Default: launches `claude -p` with the prompt
- Override: set `LAUNCH_AGENT_CMD` env var to use a different harness (receives the same three args)

## Agent Prompts

The prompts are the most important part of this design. They guide agents to follow the workflow, read the right inputs, and chain correctly.

### `scripts/prompts/implement.md`

Tells the implementing agent:

- Read the ticket (`.tickets/{ticket}.md`) for requirements, design, and acceptance criteria
- Read `git log` for context on previous review feedback (if any)
- Implement the work, run tests, commit
- If the ticket is too large for one pass: create new tickets for remaining work, adjust the dependency chain so they replace this ticket's position, and commit the new ticket files
- When done: run `just review {ticket}` as the final action
- Do NOT run `just finish-work` — that's the reviewer's job
- Do NOT include "notes to the reviewer" in commits or ticket — the code and ticket speak for themselves

### `scripts/prompts/review.md`

Tells the review agent:

- Read the ticket for requirements and acceptance criteria
- Review the diff: `git diff main...HEAD`
- Run tests: `just test`
- Check that new ticket files (if any) correctly split remaining work and maintain the dependency chain
- **To approve**: run `just finish-work {ticket}`
- **To reject**: edit the ticket to clarify requirements or note issues (commit the edit), then run `just implement {ticket}`
- Base the review on the ticket and the code — not on any narrative from the implementing agent
- The commit history (`git log main..HEAD`) shows what happened, but the ticket is the source of truth for what *should* happen

## Agent Flow

```
start-work {ticket}
  +-- create worktree
  +-- launch-agent implement {ticket} {dir}
       +-- agent implements, commits
       +-- (optionally splits ticket)
       +-- runs: just review {ticket}
            +-- launch-agent review {ticket} {dir}
                 +-- reviews ticket + diff + tests
                 |-- APPROVE: just finish-work {ticket}
                 |     +-- close ticket, rebase, merge, cleanup
                 +-- REJECT: edit ticket, just implement {ticket}
                       +-- launch-agent implement {ticket} {dir}
                            +-- (cycle continues)
```

## External Automation

A cron job or monitoring loop can:

1. List worktrees (`git worktree list`)
2. For each non-main worktree, check for an active agent process
3. If no active agent, run `just review {ticket}` — review is always a safe re-entry point (it either approves completed work or restarts implementation)

## Error Messages

Scripts should produce clear, actionable error messages that guide agents (and humans) to the right command:

- `implement` with no worktree: "No worktree for {ticket}. Use 'just start-work {ticket}' to create one."
- `review` with no worktree: "No worktree for {ticket}. Nothing to review."
- `start-work` when worktree already exists: "Worktree already exists for {ticket}. Use 'just implement {ticket}' to resume work or 'just review {ticket}' to review."
- `next-ticket` with nothing available: "No tickets ready for work." (exit 1)

## Changes to `finish-work`

Minimal. The existing script already closes the ticket idempotently. No structural changes needed — it continues to handle: close ticket, rebase, merge, remove worktree, delete branch.

## Changes to AGENTS.md

Update the "Finishing Work" section to describe the new flow. Agents should understand:

- They don't call `finish-work` directly (the reviewer does)
- They call `just review {ticket}` when done implementing
- If they're reviewing, they either approve (`finish-work`) or reject (edit ticket + `implement`)
