# UI-2 mockups (layout C)

Versioned visual references for UI-2 (browse-to-play) and the Lisimflix logo.
They settle spec decision D13 (§14). Approved by Pete, 2026-10-27.

Source canvas: [Lisimflix UI-2 Mockups](https://claude.ai/artifact/Tz52STjr8PyVdc9tbf6stM)
(Claude Design, shared with Pete's organisation). These PNGs are the reference
of record; the canvas may change later.

| File | Shows |
|---|---|
| [`logo.png`](logo.png) | Logo: rounded-square "L" with a play button in its corner, lowercase two-tone `lisimflix` wordmark; 16/32/64 px and a light variant |
| [`home-desktop.png`](home-desktop.png) | Home: Up next card, filter chips, Continue Watching, Next Up, Recently Added rows |
| [`library.png`](library.png) | Library grid: filters, sort, A–Z jump, watched and unmatched cards |
| [`detail.png`](detail.png) | Detail page: Resume / Play from start, quality chips, version picker, related row |
| [`states.png`](states.png) | §5.7 states: loading, empty while scanning, offline error with diagnostic ID, source unavailable |
| [`row.png`](row.png) | Shared row: `<` / `>` buttons, wheel scrolls sideways |
| [`home-phone.png`](home-phone.png) | Home at phone width with a bottom tab bar |

## Decisions shown

- **Theme:** dark (`#0E0F12` ground), accent periwinkle `#7C9CFF`; Outfit for headings, Inter for text.
- **Rows:** no visible native scrollbar. `<` / `>` buttons fade in on hover or keyboard
  focus and hide at the row's ends; a vertical mouse wheel scrolls the row sideways and
  passes through to the page at either end.
- **Scrollbars (rows and page):** thin, transparent track, a slightly lighter bar
  (`#FFFFFF2E`) that fades in on hover.
- Titles are Blender open movies or `[placeholders]`; artwork is placeholder blocks.

Details may still be adjusted in the UI-2 plan and review.
