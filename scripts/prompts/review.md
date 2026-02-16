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
