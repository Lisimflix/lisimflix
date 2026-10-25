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

1. Pick a named feature from the spec's §9.4 catalogue (e.g. `PLY-1`, `UI-1`) and
   claim its issue, or pick a bounded documentation/spec change. One feature slice
   or spec part per PR keeps reviews small; cite its acceptance IDs as well. Apply
   the catalogue's GitHub area labels to the issue and the affected subset to the PR.
2. Branch off an up-to-date `main`: `git switch main && git pull && git switch -c feat/short-name`.
3. Commit small and often.
4. Push the branch and open a PR. Fill in the template: `Closes #N` for an issue,
   or the spec section you built.
5. Another member reviews: run `/code-review` in Claude Code on the PR branch as a
   first pass, then read it yourself and approve or request changes.
   Don't merge your own PR unreviewed (except owners, see below).
6. Once approved, the author merges with **Squash and merge**. The branch is deleted automatically.

## Checks (CI)

- Before pushing code or opening its PR, run `make check` locally on the complete
  feature branch, after installing locked dependencies reproducibly (e.g. `npm ci`).
  Record the local result in the PR.
- The bootstrap PR that adds `make check` and GitHub Actions must run its completed
  check locally, with locked dependencies, before publishing. A new `pull_request`
  workflow can run from the PR merge branch before reaching the default branch.
  Require the normal second-member review and report the actual Actions result.
- GitHub Actions runs `make check` on every PR. Its independent result supplements
  the local preflight; a green remote run does not replace it. Every code PR,
  including bootstrap, must pass both the local check and its GitHub Actions run.
  If Actions did not run, investigate the trigger/settings rather than assuming
  a bootstrap exemption. See [GitHub's pull_request event documentation](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#pull_request).
- Docs-only PRs may skip local `make check`; once the workflow exists, GitHub Actions
  still runs on every PR.
- End-to-end tests (Playwright, real media) run locally only for now. Say in the PR's
  "How it was tested" whether you ran them.
- CI minutes are limited (2,000/month on the free plan), so workflows must:
  use Linux runners only (`ubuntu-latest`), cache Go modules and node packages,
  and cancel superseded runs on the same PR.

## Owners

- **Git hygiene and CI** (`.github/`, `.githooks/`, `CONTRIBUTING.md`, `CLAUDE.md`): Pete.
  The owner may self-merge docs and config changes in this area.
- **Spec, plans and code**: shared. Anyone can improve them through a PR that the
  other member reviews. If someone is actively working on a doc (open PR or assigned
  issue), don't edit it; open an issue for them instead.
- Code always needs the other member's review, whoever owns the area.

## Working together

- We plan for 2–3 contributors working in parallel. Choose an unclaimed ready
  feature yourself; work is not allocated by Pete or by fixed subsystem teams.
  Assign its issue to yourself before coding; record its feature ID, reviewer, file scope, consumed/
  produced contracts, standalone checks and integration dependencies. Do not claim
  another person's active scope. Use separate branches and working directories.
- Follow spec §§8.1–8.4 and 9.5: small agreed contracts and fixture/test harnesses let
  modules progress before the full skeleton is complete. Plans name the exact
  dependency needed, not a whole unfinished subsystem. Fixture-only proof is module
  acceptance; user outcomes need real integration evidence.
- Shared manifests/lockfiles, root build/CI, route/OpenAPI registration and migration
  ordering have one named integration owner per change. Agree shared edits first;
  update the contract and affected consumers together rather than editing in parallel.
- Don't edit a spec or plan someone else is writing. Open an issue for them instead.
- Hand-offs and decisions go in issue or PR comments, not only in chat.

## Naming

- Branches: `feat/…`, `fix/…`, `docs/…`, `chore/…`, `refactor/…`, `test/…`
- PR titles (they become the squashed commit message) use
  [Conventional Commits](https://www.conventionalcommits.org/): `feat(transcode): add HEVC remux`, `fix(web): …`.
  Scope matches the area label (`area:transcode` → `transcode`).

## Never commit

Secrets, `.env` files, media files, or build output. Test media goes in `testdata/`.
