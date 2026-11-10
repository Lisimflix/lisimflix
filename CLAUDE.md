# Lisimflix — agent rules

- Read `CONTRIBUTING.md` (workflow) and `docs/superpowers/specs/2026-10-24-lisimflix-design.md` (design) before working.
- **Feature planning gate:** Before implementing a claimed Feature ID, run `superpowers:writing-plans` against its spec requirements and any existing plan in `docs/superpowers/plans/`. Reuse an approved plan if it still covers the current scope; otherwise create or update a plan citing the Feature ID and meeting spec §8.4, and obtain approval before coding. A draft plan is not implementation approval. If the skill is unavailable, stop and report that.
- **Ownership:** Work on an issue only once it is assigned to the user (`gh issue edit N --add-assignee @me`); if it is assigned to someone else, stop and tell the user. Every feature ID has an issue; if one is missing, create it first. A bounded docs/spec change needs no issue.
- **Local CI gate:** Before pushing code or opening its PR, run `make check` locally on the completed feature branch. Every code PR needs a local pass, its GitHub Actions pass and a review by another member. Report the actual Actions result; if it did not run, find out why. Docs-only PRs may skip the local check.
- Never commit to or push `main`. Work on a branch named per `CONTRIBUTING.md` and open a PR.
- Never use `git push --no-verify` or force-push `main`.
- **Merge gate:** Merge only a PR authored by the `gh` user (`gh api user -q .login`), by squash, and only after the user's explicit yes to this question: "PR N is ready to be merged, approved by X with `check` green, no changes requested and no review threads still open. OK for me to merge it?" Confirm each fact before asking and again just before merging: `gh pr view N --json author,reviewDecision,latestReviews,statusCheckRollup`, plus GraphQL `reviewThreads { isResolved }` for threads. Each merge needs its own yes. Other members merge their own PRs.
- **Reviewers:** don't request reviewers or pass `--reviewer` when opening a PR, and plans don't name them: `.github/CODEOWNERS` requests Pete and Lammy plus one auto-assigned backup reviewer (CONTRIBUTING, Reviewers).
- PR titles use Conventional Commits; they become the squashed commit message.
- **README gate:** Before opening or approving a PR, check `README.md` against the branch (status, layout, setup, commands, checks). If the PR changes any of them, update `README.md` in the same PR.
- At the start of every session, run `git config core.hooksPath`. If it is not `.githooks`, tell the user and ask before setting it (see `CONTRIBUTING.md` one-time setup).
- At the start of every session, run `gh issue list` and `gh pr list` to see what others are working on.
- Don't edit another member's spec or plan in `docs/superpowers/` unless asked; open an issue instead.
- **Every review note blocks** and is settled in the PR; there are no non-blocking notes. Reviewing: each note, even a question, is an inline or file comment (a thread), submitted as Request changes; approve only with no notes and no open thread. Authoring: answer or fix every thread in the PR; resolve only threads you raised and leave others' reviews undismissed. File an issue only for something outside the PR's scope. Fix PRs for an existing review-notes issue say `Refs #N` and tick boxes (CONTRIBUTING, Workflow steps 5–7 and Working together).
- Code with no production caller yet gets `// Planned: <Feature ID> (<what will call it>).`; before calling code unused, also grep `docs/` (spec, plans).
- Never commit secrets, `.env` files or media.
