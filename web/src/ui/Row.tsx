import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Link } from "../app/router";

interface Props<T> {
  title: string;
  seeAllHref?: string;
  items: T[];
  renderCard: (item: T) => ReactNode;
}

// A horizontal strip: no native scrollbar, arrows that fade in on hover or focus, and a wheel that scrolls sideways until an end.
export function Row<T>({ title, seeAllHref, items, renderCard }: Props<T>) {
  const strip = useRef<HTMLDivElement>(null);
  const [ends, setEnds] = useState({ start: true, end: true });

  const measure = useCallback(() => {
    const el = strip.current;
    if (!el) return;
    const start = el.scrollLeft <= 1;
    const end = el.scrollLeft + el.clientWidth >= el.scrollWidth - 1;
    setEnds((p) => (p.start === start && p.end === end ? p : { start, end }));
  }, []);

  useEffect(() => {
    const el = strip.current;
    if (!el) return;
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    // Non-passive so a vertical wheel can be turned into sideways scroll.
    const onWheel = (e: WheelEvent) => {
      if (Math.abs(e.deltaY) <= Math.abs(e.deltaX)) return;
      const max = el.scrollWidth - el.clientWidth;
      if ((e.deltaY > 0 && el.scrollLeft >= max - 1) || (e.deltaY < 0 && el.scrollLeft <= 0)) return; // at the end: the page scrolls
      e.preventDefault();
      el.scrollBy({ left: e.deltaY, behavior: "instant" });
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => {
      ro.disconnect();
      el.removeEventListener("wheel", onWheel);
    };
  }, [measure, items]);

  function scroll(dir: 1 | -1) {
    const el = strip.current!;
    const card = el.firstElementChild?.getBoundingClientRect().width ?? 0;
    el.scrollBy({ left: dir * Math.max(el.clientWidth - card, card) });
  }

  return (
    <section className="row" aria-label={title}>
      <header>
        <h2>{title}</h2>
        {seeAllHref && <Link href={seeAllHref}>See all</Link>}
      </header>
      <div className="rowwrap">
        <button type="button" className="arrow left" aria-label={`Scroll ${title} left`} hidden={ends.start} onClick={() => scroll(-1)}>
          ‹
        </button>
        <div className="strip" ref={strip} onScroll={measure}>
          {items.map(renderCard)}
        </div>
        <button type="button" className="arrow right" aria-label={`Scroll ${title} right`} hidden={ends.end} onClick={() => scroll(1)}>
          ›
        </button>
      </div>
    </section>
  );
}
