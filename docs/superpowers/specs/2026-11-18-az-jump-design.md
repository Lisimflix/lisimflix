# A–Z rail jumps within the full list (#188)

Status: design approved by Lammy (2026-11-18). Needs the plan and Pete's review (UI-2 consumer, #168 `total`).
Refs #188, #168, #113.

## Why

On a real library the A–Z rail reads as a filter. Choosing a letter sets `from`, the grid
reloads starting at that letter, everything before it is gone and the title count drops to
"that letter onwards". Viewers expect what other media servers do: the list jumps to the
letter, you can scroll up into the previous letter or down into the next, and the count
stays the library's.

## Goals

- Choosing a letter scrolls the library grid to the first title in that letter.
- From there, scrolling up and scrolling down both keep loading titles.
- The title count is the whole listing (permission and filters applied), whatever letter is chosen.
- Cost of a jump doesn't grow with how far into the library the letter is: one page request, then pages on demand.

## Non-goals

- No rail for sorts other than title (unchanged).
- No change to filters, facets, item detail or other listings.
- No offset-based paging in the API: cursors stay opaque keysets.

## Anti-goals

- A jump that downloads every title before the letter.
- Changing `total` on listings that don't use `from`.
- Breaking a client that only uses `nextCursor` (the change is additive for it).

## Approaches considered

| | How | Trade-off |
|---|---|---|
| **A. Paging both ways (chosen)** | The first page at a letter also returns how many titles come before it (`offset`) and a cursor that loads upwards (`prevCursor`). The grid sizes itself for the full count, scrolls to `offset` and fills in both ways. | Correct at any size; the most work (contract fields, upward query, grid with an unloaded region at the top). |
| B. Letter positions, forward loading | The first page returns a per-letter count; a jump loads every page before the letter, then scrolls. | Small, but a jump to Z at 5k titles fetches ~25 pages of 200, and it grows with the library. |

Lammy chose A: it matches the viewer's expectation at any library size, which is the problem #188 came from.

## Design

### API (CTR-2, `listItems`; additive)

- `from` stops being a filter and becomes a **start position**: the first page starts at the
  first title whose title group/sort key is at or after the letter (`#` is the start of the list).
- `ItemPage.offset` (new, integer or null): on a first page requested with `from`, the number
  of titles in the listing before the first item of that page. Null otherwise.
- `ItemPage.prevCursor` (new, string or null): loads the page immediately before this one, in
  the same order. Title order only (the only order with a rail); null in other orders, when
  nothing comes before, and on a first page without `from`.
  Pages loaded with a `prevCursor` also carry `prevCursor`, so a client can keep loading upwards;
  every page keeps `nextCursor` as today.
- The `cursor` parameter accepts either `nextCursor` or `prevCursor`; the cursor itself records
  the direction, so no new parameter is needed.
- `total` counts the whole listing; `from` no longer reduces it. This settles #168's
  "`total` with `from`" question by leaving `from` out of the count.
- A letter past the end (e.g. `from=Z` with nothing at or after `z`) gives `items: []`,
  `nextCursor: null`, `offset == total`, and a `prevCursor` that loads the last page.

Consumers: the web library grid (UI-2) only. The contract text, Go types, OpenAPI,
generated `schema.d.ts` and fixtures change together in one PR.

### Server

- Keep the keyset order (`sort_group`, `sort_key`, `id`). An upward page is the same query with
  the predicate reversed and the order descending, then reversed back before returning.
- `offset` is a `COUNT(*)` of the listing's `WHERE` plus "keys before the jump point", run only
  on a first page with `from`.
- The jump point for an empty page is a boundary key (the letter's group and lowercase key with
  an empty ID), so `prevCursor` still works there.

### Web

- The grid holds a contiguous loaded window `[start, start + items.length)` of a list of
  `total` slots. Slots outside the window render as placeholder cards.
- Choosing a letter loads the page at that letter, sets `start = offset`, and scrolls to that row.
- Scrolling near the top of the window loads the previous page (`prevCursor`) and prepends it;
  near the bottom, the next page as today. Prepending keeps the viewport on the same cards.
- The URL keeps `from`, so reload and Back open at the same letter.
- "Back to top" is removed: scrolling up reaches the top. `aria-current` on the rail letter stays.
- The saved view cache (scroll restore) stores `start` along with the window.

## Left to the plan

- Exact Go signatures, cursor encoding of the direction, and SQL.
- Whether a letter whose titles are already in the loaded window only scrolls (no request).
- How `VirtualGrid` takes `start` (prop shape), keyboard focus across the placeholder region,
  and placeholder look (existing `Skeleton` card if it fits).
- Test list: server (offset, both directions, boundaries `#`, past-the-end, filters, long-title
  ID-only cursors, cursor bound to the query), contract fixtures, web (jump, scroll up/down,
  count stays, restore).
- README: check the A–Z wording if it describes the rail.
