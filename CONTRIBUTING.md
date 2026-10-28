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
   If the PR changes anything `README.md` describes (status, layout, setup,
   commands or checks), update `README.md` in the same PR.
5. Another member (see Reviewers below) reviews: run `/code-review` in Claude Code on the PR branch as a
   first pass, then read it yourself and approve or request changes.
   Check that `README.md` still matches the branch before approving.
6. Once approved, the author merges with **Squash and merge**. The branch is deleted automatically.

`main` is protected by a GitHub ruleset with no bypass, so GitHub enforces the steps
above: no direct or force pushes, and a PR merges only by squash, with one approval
and a passing `check`. This applies to every PR, including docs, config and Dependabot.
An admin can disable the ruleset (Settings → Rules) for an emergency; say why on the
PR and turn it back on straight after.

## Checks (CI)

- Before pushing code or opening its PR, run `make check` locally on the complete
  feature branch, after installing locked dependencies reproducibly (e.g. `npm ci`).
  Record the local result in the PR.
- The bootstrap PR that adds `make check` and GitHub Actions must run its completed
  check locally, with locked dependencies, before publishing. A new `pull_request`
  workflow can run from the PR merge branch before reaching the default branch.
  Require the normal review by another member and report the actual Actions result.
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
- Dependabot (`.github/dependabot.yml`) opens weekly update PRs for Go modules, npm,
  Dockerfile base images and Actions: minor/patch bumps grouped per ecosystem, majors
  alone. Review them like any PR; `make check` must pass. A dependency added outside
  these (e.g. a binary installed in the `Dockerfile`) needs a pinned version and a
  note on how it gets updated.

## Owners

- **Git hygiene and CI** (`.github/`, `.githooks/`, `CONTRIBUTING.md`, `CLAUDE.md`, the
  `main` ruleset): Pete.
- **Spec, plans and code**: shared. Anyone can improve them through a PR that
  another member reviews. If someone is actively working on a doc (open PR or assigned
  issue), don't edit it; open an issue for them instead.
- Code always needs another member's review, whoever owns the area.

## Reviewers

- Main reviewers: Pete (`lisim`) and Lammy (`Lammy93`).
- Ondo (`ondona`) and Millzy (`Millzy99`) can also review and approve.
- One approval from any of them is enough, as long as it isn't the PR's author.
  Never approve your own PR.
- You don't need to request anyone: `.github/CODEOWNERS` requests the `reviewers`
  team on every PR, and GitHub assigns one member, spreading reviews evenly. Set
  your GitHub status to Busy while you're away and you'll be skipped.
- The assigned member is expected to review, but anyone on the team can review and
  approve at any time. If the assigned reviewer hasn't responded within 24 hours,
  someone else should pick it up.

## Working together

- We plan for 2–4 contributors working in parallel. Choose an unclaimed ready
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
- Non-blocking review notes, and issues noticed in passing, get their own issue
  (area labels, linking the PR or review) before the PR merges. A comment alone gets lost.
- Code added for a later feature, with no production caller yet, gets a one-line comment
  naming its future user: `// Planned: <Feature ID> (<what will call it>).` Remove it when
  the caller lands. Reviews and audits treat untagged code with no caller as dead.

## Naming

- Branches: `feat/…`, `fix/…`, `docs/…`, `chore/…`, `refactor/…`, `test/…`
- PR titles (they become the squashed commit message) use
  [Conventional Commits](https://www.conventionalcommits.org/): `feat(transcode): add HEVC remux`, `fix(web): …`.
  Scope matches the area label (`area:transcode` → `transcode`).

## Never commit

Secrets, `.env` files, media files, or build output. Test media goes in `testdata/`.
