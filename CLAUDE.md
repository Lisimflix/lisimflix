# Lisimflix — agent rules

- Read `CONTRIBUTING.md` (workflow) and `docs/superpowers/specs/2026-10-24-lisimflix-design.md` (design) before working.
- Never commit to or push `main`. Work on a branch named per `CONTRIBUTING.md` and open a PR.
- Never use `git push --no-verify`, force-push `main`, or merge a PR yourself.
- PR titles use Conventional Commits; they become the squashed commit message.
- At the start of every session, run `git config core.hooksPath`. If it is not `.githooks`, tell the user and ask before setting it (see `CONTRIBUTING.md` one-time setup).
- Never commit secrets, `.env` files or media.
