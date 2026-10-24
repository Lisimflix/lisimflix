# Contributing to Lisimflix

Design: [`docs/superpowers/specs/2026-10-24-lisimflix-design.md`](docs/superpowers/specs/2026-10-24-lisimflix-design.md).

## One-time setup

```sh
gh repo clone Lisimflix/lisimflix
cd lisimflix
git config core.hooksPath .githooks
```

The pre-push hook refuses pushes to `main`. Our GitHub plan can't protect
`main` on a private repo, so this hook plus the rules below is the protection.
Never bypass it with `--no-verify`.

## Workflow

1. Pick the work: **either** an issue, **or** one part of the design spec
   (e.g. "§4.4 Subtitles"). One issue or one spec part per PR, so PRs stay small.
2. Branch off an up-to-date `main`: `git switch main && git pull && git switch -c feat/short-name`.
3. Commit small and often.
4. Push the branch and open a PR. Fill in the template: `Closes #N` for an issue,
   or the spec section you built.
5. Another member reviews: run `/code-review` in Claude Code on the PR branch as a
   first pass, then read it yourself and approve or request changes.
   Don't merge your own PR unreviewed.
6. Once approved, the author merges with **Squash and merge**. The branch is deleted automatically.

## Checks (CI)

- Before opening a PR, run `make check` locally (lint, unit tests, web build).
  It arrives with the first code PR; docs-only PRs need no checks until then.
- GitHub Actions runs the same `make check` on every PR. A PR merges only when it's green.
- End-to-end tests (Playwright, real media) run locally only for now. Say in the PR's
  "How it was tested" whether you ran them.
- CI minutes are limited (2,000/month on the free plan), so workflows must:
  use Linux runners only (`ubuntu-latest`), cache Go modules and node packages,
  and cancel superseded runs on the same PR.

## Working together

- Assign an issue to yourself before starting it, so two people (or two Claudes)
  never edit the same files.
- Don't edit a spec or plan someone else is writing. Open an issue for them instead.
- Hand-offs and decisions go in issue or PR comments, not only in chat.

## Naming

- Branches: `feat/…`, `fix/…`, `docs/…`, `chore/…`, `refactor/…`, `test/…`
- PR titles (they become the squashed commit message) use
  [Conventional Commits](https://www.conventionalcommits.org/): `feat(transcode): add HEVC remux`, `fix(web): …`.
  Scope matches the area label (`area:transcode` → `transcode`).

## Never commit

Secrets, `.env` files, media files, or build output. Test media goes in `testdata/`.
