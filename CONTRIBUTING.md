# Contributing to Lisimflix

Design: [`docs/superpowers/specs/2026-10-24-lisimflix-design.md`](docs/superpowers/specs/2026-10-24-lisimflix-design.md).

## One-time setup

```sh
gh repo clone Lisimflix/lisimflix
cd lisimflix
git config core.hooksPath .githooks
```

The pre-push hook refuses pushes to `main`, as a local check before the GitHub
ruleset (below) rejects them. Never bypass it with `--no-verify`.

## Workflow

1. Pick a named feature from the spec's §9.4 catalogue (e.g. `PLY-1`, `UI-1`) or another
   open issue, and assign the issue to yourself before you start. Every feature ID has an
   issue; if one is missing, create it first. A bounded documentation/spec change needs
   no issue. One feature slice
   or spec part per PR keeps reviews small; cite its acceptance IDs as well. Apply
   the catalogue's GitHub area labels to the issue and the affected subset to the PR.
2. Branch off an up-to-date `main`: `git switch main && git pull && git switch -c feat/short-name`.
3. Commit small and often.
4. Push the branch and open a PR. Fill in the template: `Closes #N` for an issue,
   or the spec section you built.
   If the PR changes anything `README.md` describes (status, layout, setup,
   commands or checks), update `README.md` in the same PR.
5. Another member (see Reviewers below) reviews: run `/code-review` in Claude Code on the PR branch as a
   first pass, then read it yourself. Check that `README.md` still matches the branch.
   Every note blocks, even a question: put each one on the PR as an inline or file comment
   (a thread), including the `/code-review` findings you've checked, and submit the review as
   **Request changes**. Approve only when you have no notes and no thread is open.
6. The author answers or fixes every thread in the PR. Whoever raised a thread resolves it,
   and only they dismiss their own review. The reviewer then re-reviews (step 5).
7. Once approved, the author merges with **Squash and merge**. The branch is deleted automatically.
   Any push after approval (fixups, rebases) needs a fresh approval before it can merge.
   Dependabot can't merge its own PRs: whoever approves one merges it.

`main` is protected by a GitHub ruleset with no bypass, so GitHub enforces the steps
above: no direct or force pushes, and a PR merges only by squash, with a passing `check`,
every review thread resolved, no outstanding request for changes, and one approval of its
latest push from someone other than whoever pushed it.
This applies to every PR, including docs, config and Dependabot. `check` also runs on
`main` after every merge.
An admin can disable the ruleset (Settings → Rules) for an emergency; say why on the
PR and turn it back on straight after.

## Checks (CI)

- Before pushing code or opening its PR, run `make check` locally on the complete
  feature branch, after installing locked dependencies reproducibly (e.g. `npm ci`).
  Record the local result in the PR.
- GitHub Actions runs `make check` on every PR. Its independent result supplements
  the local preflight; a green remote run does not replace it. Every code PR must pass
  both the local check and its GitHub Actions run. If Actions did not run, find out why
  before merging.
- Docs-only PRs may skip local `make check`; GitHub Actions still runs on every PR.
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
- You don't need to request anyone: `.github/CODEOWNERS` requests Pete and Lammy on
  every PR, plus one of Ondo or Millzy (the `backup-reviewers` team; GitHub assigns
  one, spreading reviews evenly). Set your GitHub status to Busy while you're away
  and you'll be skipped.
- Any requested reviewer can review and approve at any time. If nobody has responded
  within 24 hours, the backup reviewer should pick it up.

## Working together

- We plan for 2–4 contributors working in parallel. Choose an unclaimed ready
  feature yourself; work is not allocated by Pete or by fixed subsystem teams.
  Assign its issue to yourself before coding; record its feature ID, file scope, consumed/
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
- Every review note blocks and is settled in the PR as a thread before approval
  (Workflow, steps 5–6). There are no non-blocking notes, so nothing is left to go stale.
  - File an issue only for something outside the PR's scope, such as a problem noticed
    in passing in code the PR doesn't touch (area labels, linking the PR). It does not
    block the merge.
  - A PR that fixes part of an existing review-notes issue says `Refs #N` and ticks its
    boxes. The issue closes once every box is ticked or dropped with a reason.
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
