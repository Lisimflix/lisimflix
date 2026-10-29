import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type FocusEvent, type KeyboardEvent, type ReactNode } from "react";

export interface VirtualGridProps<T extends { id: string }> {
  items: T[];
  total: number | null;
  hasMore: boolean;
  loading: boolean;
  loadMore: () => void;
  renderCard: (item: T, index: number) => ReactNode;
  cardWidth?: number;
  gap?: number;
  cardAspect?: number;
  captionHeight?: number;
  overscanRows?: number;
}

interface Metrics { width: number; top: number; scrollY: number; vh: number }

// Window-scroll virtual grid: only the rows near the viewport are in the DOM, inside a spacer sized from `total`.
export function VirtualGrid<T extends { id: string }>({
  items, total, hasMore, loading, loadMore, renderCard,
  cardWidth = 180, gap = 16, cardAspect = 1.5, captionHeight = 52, overscanRows = 2,
}: VirtualGridProps<T>) {
  const ref = useRef<HTMLDivElement>(null);
  const pending = useRef<number | null>(null);
  const active = useRef(0); // roving tabindex: the one card in the tab order
  const focused = useRef<number | null>(null); // index of the card holding focus; kept when it scrolls out of the window
  const [m, setM] = useState<Metrics>({ width: 0, top: 0, scrollY: 0, vh: 0 });

  const measure = () => {
    const el = ref.current;
    if (!el) return;
    const next = { width: el.clientWidth, top: el.getBoundingClientRect().top + window.scrollY, scrollY: window.scrollY, vh: window.innerHeight };
    setM((p) => (p.width === next.width && p.top === next.top && p.scrollY === next.scrollY && p.vh === next.vh ? p : next));
  };
  useLayoutEffect(measure); // also catches layout shifts above the grid, e.g. a banner appearing
  useEffect(() => {
    window.addEventListener("scroll", measure, { passive: true });
    window.addEventListener("resize", measure);
    return () => {
      window.removeEventListener("scroll", measure);
      window.removeEventListener("resize", measure);
    };
  }, []);

  const cols = Math.max(1, Math.floor((m.width + gap) / (cardWidth + gap)));
  const cardW = (m.width - gap * (cols - 1)) / cols;
  const rowH = cardW * cardAspect + captionHeight + gap;
  const count = Math.max(total ?? 0, items.length);
  const rows = Math.ceil(count / cols);
  const rel = m.scrollY - m.top;
  const clamp = (n: number) => Math.min(Math.max(n, 0), Math.max(rows - 1, 0));
  const first = m.width ? clamp(Math.floor(rel / rowH) - overscanRows) : 0;
  const last = m.width ? clamp(Math.floor((rel + m.vh) / rowH) + overscanRows) : 0;
  const loadedRows = Math.ceil(items.length / cols);

  // Runs every render; loadMore itself refuses while a load is pending.
  useEffect(() => {
    if (m.width && hasMore && !loading && last >= loadedRows - 3) loadMore();
  });

  // A focus target on a row that wasn't rendered yet: focus it once the scroll has rendered it.
  useEffect(() => {
    if (pending.current === null) return;
    const cell = ref.current?.querySelector<HTMLElement>(`[data-index="${pending.current}"]`);
    if (cell) {
      pending.current = null;
      cell.querySelector<HTMLElement>("a,button")?.focus();
    }
  });

  // One tab stop: the active card, or the first rendered one while the active card is outside the window.
  // Also puts focus back on a card that was scrolled out of the window and has just been rendered again.
  useLayoutEffect(() => {
    const links = Array.from(ref.current?.querySelectorAll<HTMLElement>(".cell:not(:has(.ph)) > a, .cell:not(:has(.ph)) > button") ?? []);
    const stop = links.find((a) => Number(a.closest<HTMLElement>("[data-index]")!.dataset.index) === active.current) ?? links[0];
    links.forEach((a) => { a.tabIndex = a === stop ? 0 : -1; });
    const back = focused.current === null ? undefined : links.find((a) => Number(a.closest<HTMLElement>("[data-index]")!.dataset.index) === focused.current);
    if (back && document.activeElement === document.body) back.focus({ preventScroll: true });
  });

  function onFocus(e: FocusEvent<HTMLDivElement>) {
    const cell = (e.target as HTMLElement).closest<HTMLElement>("[data-index]");
    if (!cell) return;
    active.current = focused.current = Number(cell.dataset.index);
    ref.current?.querySelectorAll<HTMLElement>("[tabindex]").forEach((a) => { a.tabIndex = a === e.target ? 0 : -1; });
  }

  // A card removed while focused sends no blur, so a blur seen on a connected card means focus really moved on.
  function onBlur(e: FocusEvent<HTMLDivElement>) {
    if ((e.target as HTMLElement).isConnected) focused.current = null;
  }

  function onKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    const from = (e.target as HTMLElement).closest<HTMLElement>("[data-index]");
    const delta = ({ ArrowRight: 1, ArrowLeft: -1, ArrowDown: cols, ArrowUp: -cols } as Record<string, number>)[e.key];
    if (!from || delta === undefined || e.altKey || e.ctrlKey || e.metaKey) return;
    e.preventDefault();
    const to = Math.min(Math.max(Number(from.dataset.index) + delta, 0), items.length - 1);
    pending.current = to;
    const cell = ref.current?.querySelector<HTMLElement>(`[data-index="${to}"]`);
    if (cell) {
      pending.current = null;
      cell.querySelector<HTMLElement>("a,button")?.focus();
    } else {
      window.scrollTo(0, m.top + Math.floor(to / cols) * rowH - 80);
    }
  }

  const start = first * cols;
  const end = Math.min((last + 1) * cols, count);
  const style = { "--caption": `${captionHeight}px`, "--aspect": `1 / ${cardAspect}` } as CSSProperties;
  return (
    <div ref={ref} className="vgrid" style={{ ...style, height: Math.max(rows * rowH - gap, 0) }} onKeyDown={onKeyDown} onFocus={onFocus} onBlur={onBlur}>
      {m.width > 0 && (
        <div className="vgrid-rows" style={{ top: first * rowH, gridTemplateColumns: `repeat(${cols}, ${cardW}px)`, gap }}>
          {Array.from({ length: Math.max(end - start, 0) }, (_, k) => {
            const i = start + k;
            const item = items[i];
            return (
              <div key={item ? item.id : `ph-${i}`} data-index={i} className="cell">
                {item ? renderCard(item, i) : <div className="card ph" aria-hidden="true"><span className="art" /><span className="cap" /></div>}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
