---
name: update-progress
description: Use when the user asks to refresh, update, or check the implementation progress doc (docs/implementation-progress.md on the docs branch), or after finishing a unit of work where it's unclear whether a milestone was completed.
---

# Updating the implementation progress doc

`docs/implementation-progress.md` lives on the `docs` branch and is edited only through the
dedicated worktree at `../MindMesh-docs`. Never check out `docs` in the main worktree.

## Steps

1. **Confirm the worktree exists.** Run `git worktree list` from the main worktree. If
   `../MindMesh-docs` isn't listed, create it: `git worktree add ../MindMesh-docs docs`.

2. **Read current state.** Read `../MindMesh-docs/docs/implementation-progress.md` and
   `docs/detailed/19-build-roadmap.md` (read via `git show docs:docs/detailed/19-build-roadmap.md`
   from the main worktree, since that file isn't checked out there). Check recent commits
   (`git log --oneline -20`) and which `internal/<service>` packages exist and pass
   `go test ./...` to ground status claims in actual repo state, not assumption.

3. **Diff status against reality.** For each phase/service in the doc, check whether its
   status marker (`✅`/`🟡`/`⬜`) and "Remaining" notes still match the code. Don't mark
   something `✅ Done` unless `CLAUDE.md`'s "Definition of done per service" is actually met for
   its current scope.

4. **MVP-scoping decisions need prior confirmation.** If you're about to mark something `🟡
   Partial` because of a *deliberate* scope reduction (not just "not built yet"), check whether
   the user already confirmed that scoping decision earlier in this conversation. If not, ask
   the user before recording it — don't infer or invent a scoping decision.

5. **Edit and commit in the worktree, not the main one.**

   ```bash
   cd ../MindMesh-docs
   # edit docs/implementation-progress.md here
   git add docs/implementation-progress.md
   git commit -m "docs: update implementation progress"
   ```

   Never `git push` from the worktree unless the user explicitly asks.

6. **Report back** what changed (or that nothing needed updating) in one or two sentences.
