# Lisimflix — agent rules

- Read `CONTRIBUTING.md` (workflow) and `docs/superpowers/specs/2026-10-24-lisimflix-design.md` (design) before working.
- **Feature planning gate:** Before implementing a claimed Feature ID, run `superpowers:writing-plans` against its spec requirements and any existing plan in `docs/superpowers/plans/`. Reuse an approved plan if it still covers the current scope; otherwise create or update a plan citing the Feature ID and meeting spec §8.4, and obtain approval before coding. A draft plan is not implementation approval. If the skill is unavailable, stop and report that.
- **Local CI gate:** Before pushing code or opening its PR, run `make check` locally on the completed feature branch. Every code PR, including the initial CI-bootstrap PR, needs both a local pass and its GitHub Actions pass plus a review by another member. A new `pull_request` workflow can run from the PR merge branch before reaching the default branch. Report the actual Actions result; if it did not run, investigate the trigger/settings rather than assuming a bootstrap exemption. Docs-only PRs may skip the local check.
- Never commit to or push `main`. Work on a branch named per `CONTRIBUTING.md` and open a PR.
- Never use `git push --no-verify`, force-push `main`, or merge a PR yourself.
- **Reviewers:** don't request reviewers or pass `--reviewer` when opening a PR, and plans don't name them: `.github/CODEOWNERS` requests Pete and Lammy plus one auto-assigned backup reviewer (CONTRIBUTING, Reviewers).
- PR titles use Conventional Commits; they become the squashed commit message.
- **README gate:** Before opening or approving a PR, check `README.md` against the branch (status, layout, setup, commands, checks). If the PR changes any of them, update `README.md` in the same PR.
- At the start of every session, run `git config core.hooksPath`. If it is not `.githooks`, tell the user and ask before setting it (see `CONTRIBUTING.md` one-time setup).
- At the start of every session, run `gh issue list` and `gh pr list` to see what others are working on.
- Don't edit another member's spec or plan in `docs/superpowers/` unless asked; open an issue instead.
- Review notes are blocking (anything that must change before merge, however small: request changes, never approve with fixes pending) or non-blocking (one issue per review, a checkbox per checked note, area labels, linking the PR/review, filed before the PR merges; no nits). Issues noticed in passing get an issue the same way. Fix PRs say `Refs #N` and tick boxes; the issue closes when every box is ticked or dropped with a reason (CONTRIBUTING, Working together).
- Code with no production caller yet gets `// Planned: <Feature ID> (<what will call it>).`; before calling code unused, also grep `docs/` (spec, plans).
- Never commit secrets, `.env` files or media.
