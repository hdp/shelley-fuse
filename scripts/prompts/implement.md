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
