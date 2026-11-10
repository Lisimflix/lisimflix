---
name: repo-check
description: Check the repo. Reports every open PR and issue with its current state as numbered options in the order Merge, Review, Fix, New feature. Use when asked to check the repo or what to work on next, and at the start of a session.
---

# Repo check

Read-only: gather, sort, report.

## 1. Gather

```sh
gh api user -q .login
gh api graphql -f query='{ repository(owner: "Lisimflix", name: "lisimflix") { pullRequests(states: OPEN, first: 100) { nodes {
  number title isDraft mergeable mergeStateStatus reviewDecision headRefOid author { login }
  commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
  latestOpinionatedReviews(first: 20) { nodes { author { login } state commit { oid } } }
  reviewRequests(first: 20) { nodes { requestedReviewer { ... on User { login } ... on Team { name } } } }
  reviewThreads(first: 100) { nodes { isResolved first: comments(first: 1) { nodes { author { login } } } last: comments(last: 1) { nodes { author { login } } } } }
} } } }'
gh issue list --state open --limit 500 --json number,title,assignees,labels
```

`mergeable` or `mergeStateStatus` of `UNKNOWN` means GitHub is still computing it: run the query once more. A null `statusCheckRollup` means no checks ran.

Done when you hold the login, every open PR and every open issue.

## 2. Sort

"You" is the `gh` login. Put each PR and each issue in exactly one group, the first that fits.

**Ready** means all of: not a draft, `mergeStateStatus` CLEAN, `reviewDecision` APPROVED, check state SUCCESS, zero unresolved threads.

1. **Merge**: your PRs that are ready.
2. **Review**: other members' non-draft PRs that wait on you:
   - you have not reviewed it (first review);
   - your latest review is on a commit older than `headRefOid` (re-review after a push);
   - a thread you raised is unresolved and its last comment is by someone else (answer waiting for you).
3. **Fix**
   - Your PRs with something for you to do, each with every reason: changes requested on the current `headRefOid`, N unresolved threads whose last comment is by someone else, check failing or not run, conflict, draft.
   - Then open issues assigned to you.
   - Then unassigned issues without the `feature` label (bugs, review notes, chores): assign before starting.
4. **New feature**: open issues labelled `feature` with no assignee.
5. **Other**: what waits on someone else: your other not-ready PRs, each with who it waits on (first review: name who is requested; re-review after your push; your thread replies; check pending), other members' PRs where you requested changes and nothing has moved since, ones you have approved (ready ones: the author merges), their drafts, and issues assigned to others.

Done when the group sizes add up to the PR count plus the issue count.

## 3. Report

Lead with the single top action: the first item of the first non-empty group. Then the groups in order, one line per item: number, title, state, the action it needs. State the totals and the group sizes so the user can see nothing is missing.

- **Merge** items follow the Merge gate in `CLAUDE.md`: ask its question, one PR at a time.
- **Review** items: the user reviews; offer `/code-review` on that PR as the first pass. Your own PRs are reviewed by other members.
- A group longer than ten lines may be summarised by count after its first ten; list the rest on request.

End with numbered options the user can pick, in the same priority order. When the session opened with a different request, give the report and then carry on with that request.
