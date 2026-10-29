import { useEffect, useRef, useState } from "react";
import { ApiError, type Client, type ItemQuery, type ItemSummary, type Library as LibraryInfo, type LibraryFacets } from "../api/client";
import { cached } from "../app/cache";
import { navigate, useSearch } from "../app/router";
import { Card } from "../ui/Card";
import { Chip, MenuChip, type Option } from "../ui/Chips";
import { VirtualGrid } from "../ui/VirtualGrid";

const PAGE = 50;
const SORTS = [
  { value: "title", label: "Title A–Z" },
  { value: "added", label: "Recently added" },
  { value: "year", label: "Year, newest first" },
] as const;
const RESOLUTIONS = new Map([["sd", "SD"], ["720p", "720p"], ["1080p", "1080p"], ["4k", "4K"]]);
const LETTERS = ["#", ..."ABCDEFGHIJKLMNOPQRSTUVWXYZ"];

// shortcut: 639-2→1 table covers common codes only; widen when a library shows raw codes
const ISO639_1: Record<string, string> = {
  eng: "en", jpn: "ja", deu: "de", ger: "de", fra: "fr", fre: "fr", spa: "es", ita: "it", por: "pt", rus: "ru",
  zho: "zh", chi: "zh", kor: "ko", nld: "nl", dut: "nl", swe: "sv", nor: "no", dan: "da", fin: "fi", pol: "pl",
  tur: "tr", ara: "ar", heb: "he", hin: "hi", tha: "th", ces: "cs", cze: "cs", hun: "hu", ell: "el", gre: "el",
  ukr: "uk", vie: "vi", ind: "id", ron: "ro", rum: "ro", bul: "bg", hrv: "hr", fas: "fa", per: "fa", cat: "ca",
};
const languages = new Intl.DisplayNames(["en"], { type: "language" });
export function languageName(code: string): string {
  const two = ISO639_1[code];
  if (!two) return code;
  try {
    return languages.of(two) ?? code;
  } catch {
    return code;
  }
}

// Everything the grid shows lives in one object, so it can be stored and restored whole (decision 12).
export interface GridState {
  key: string;
  items: ItemSummary[];
  cursor: string | null;
  total: number | null;
  hasMore: boolean;
  loading: boolean;
  error: string | null;
}
const fresh = (key: string): GridState => ({ key, items: [], cursor: null, total: null, hasMore: true, loading: false, error: null });

// Filter and sort state is the URL query, so back, forward and reload keep it.
function queryFrom(libraryId: string, p: URLSearchParams, noWatched: boolean): ItemQuery {
  const sortParam = p.get("sort");
  const sort = SORTS.find((s) => s.value === sortParam)?.value ?? "title";
  const from = p.get("from");
  const num = (k: string) => (/^\d{1,4}$/.test(p.get(k) ?? "") ? Number(p.get(k)) : undefined);
  const res = p.get("resolution");
  const q: ItemQuery = { libraryId, sort };
  if (sort === "title" && from && /^[A-Z#]$/.test(from)) q.from = from as ItemQuery["from"];
  if (p.get("genre")) q.genre = p.get("genre")!;
  if (num("yearFrom") !== undefined) q.yearFrom = num("yearFrom");
  if (num("yearTo") !== undefined) q.yearTo = num("yearTo");
  if (res && RESOLUTIONS.has(res)) q.resolution = res as ItemQuery["resolution"];
  if (p.get("hdr") === "true") q.hdr = true;
  if (p.get("audioLanguage")) q.audioLanguage = p.get("audioLanguage")!;
  if (p.get("watched") === "false" && !noWatched) q.watched = false;
  return q;
}

export function Library({ client, library }: { client: Client; library: LibraryInfo }) {
  const search = useSearch();
  const params = new URLSearchParams(search);
  const [noWatched, setNoWatched] = useState(false); // LIB-2c answers `watched` with 400 until DATA-1 is live
  const query = queryFrom(library.id, params, noWatched);
  const key = JSON.stringify(query);
  const [facets, setFacets] = useState<LibraryFacets | null>(null);
  const [state, setState] = useState<GridState>(() => fresh(key));
  const s = state.key === key ? state : fresh(key);
  const live = useRef({ key, busy: false });
  live.current.key = key;

  useEffect(() => {
    let on = true;
    setFacets(null);
    cached(`facets:${library.id}`, () => client.facets(library.id)).then((f) => on && setFacets(f), () => undefined);
    return () => { on = false; };
  }, [client, library.id]);

  const firstKey = useRef(key);
  useEffect(() => {
    if (firstKey.current !== key) window.scrollTo(0, 0);
    firstKey.current = key;
  }, [key]);

  function loadMore() {
    if (live.current.busy || s.loading || !s.hasMore || s.error) return;
    const base = s;
    live.current.busy = true;
    setState({ ...base, loading: true });
    client
      .items({ ...query, limit: PAGE, ...(base.cursor ? { cursor: base.cursor } : {}) })
      .then(
        (page) => {
          if (live.current.key !== key) return;
          setState({
            key,
            items: [...base.items, ...page.items],
            cursor: page.nextCursor,
            total: page.total ?? base.total,
            hasMore: page.nextCursor !== null,
            loading: false,
            error: null,
          });
        },
        (err: unknown) => {
          if (live.current.key !== key) return;
          if (err instanceof ApiError && err.status === 400 && query.watched !== undefined) {
            setNoWatched(true);
            return;
          }
          setState({ ...base, loading: false, error: err instanceof ApiError ? err.message : "Something went wrong." });
        },
      )
      .finally(() => {
        live.current.busy = false;
        setState((p) => ({ ...p })); // wake the grid in case the key changed while this was in flight
      });
  }

  // shortcut: jump restarts the list; upgrade to bidirectional paging if Pete finds it awkward at 5k titles
  // (`from` is part of the query key, so setting it resets items and cursor and restarts the grid at that letter)
  function setParams(patch: Record<string, string | null>) {
    const p = new URLSearchParams(location.search);
    for (const [k, v] of Object.entries(patch)) {
      if (v === null) p.delete(k);
      else p.set(k, v);
    }
    if ((p.get("sort") ?? "title") !== "title") p.delete("from");
    const qs = p.toString();
    navigate(location.pathname + (qs ? `?${qs}` : ""));
  }

  const sort = query.sort ?? "title";
  const year = query.yearFrom !== undefined && query.yearFrom === query.yearTo ? String(query.yearFrom) : null;
  const filtered = query.genre || year || query.resolution || query.hdr || query.audioLanguage || query.watched !== undefined;
  const years: Option[] = facets?.years
    ? Array.from({ length: facets.years.max - facets.years.min + 1 }, (_, i) => String(facets.years!.max - i)).map((y) => ({ value: y, label: y }))
    : [];

  return (
    <>
      <h1>{library.name}</h1>
      {s.total !== null && <p className="count">{s.total === 1 ? "1 title" : `${s.total} titles`}</p>}
      {library.availability === "partial" && <p role="status" className="banner">Some titles are offline</p>}
      <div className="filters" role="group" aria-label="Filters">
        <Chip label="All" pressed={!filtered} onClick={() => setParams({ genre: null, yearFrom: null, yearTo: null, resolution: null, hdr: null, audioLanguage: null, watched: null })} />
        {!noWatched && <Chip label="Unwatched" pressed={query.watched === false} onClick={() => setParams({ watched: query.watched === false ? null : "false" })} />}
        {facets && facets.genres.length > 0 && (
          <MenuChip label="Genre" value={query.genre ?? null} options={facets.genres.map((g) => ({ value: g, label: g }))} onChange={(v) => setParams({ genre: v })} />
        )}
        {years.length > 0 && (
          <MenuChip label="Year" value={year} options={years} onChange={(v) => setParams({ yearFrom: v, yearTo: v })} />
        )}
        {facets && facets.resolutions.length > 0 && (
          <MenuChip label="Resolution" value={query.resolution ?? null} options={facets.resolutions.map((r) => ({ value: r, label: RESOLUTIONS.get(r) ?? r }))} onChange={(v) => setParams({ resolution: v })} />
        )}
        {facets?.hdr && <Chip label="HDR" pressed={query.hdr === true} onClick={() => setParams({ hdr: query.hdr ? null : "true" })} />}
        {facets && facets.audioLanguages.length > 0 && (
          <MenuChip label="Audio language" value={query.audioLanguage ?? null} options={facets.audioLanguages.map((l) => ({ value: l, label: languageName(l) }))} onChange={(v) => setParams({ audioLanguage: v })} />
        )}
        <label className="sort">
          <span className="sr">Sort</span>
          <select aria-label="Sort" value={sort} onChange={(e) => setParams({ sort: e.target.value, from: null })}>
            {SORTS.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
          </select>
        </label>
        {query.from && <button type="button" className="chip" onClick={() => setParams({ from: null })}>Back to top</button>}
      </div>
      {s.error && (
        <p role="alert">
          {s.error} <button type="button" onClick={() => setState({ ...s, error: null })}>Retry</button>
        </p>
      )}
      <VirtualGrid
        items={s.items}
        total={s.total}
        hasMore={s.hasMore && !s.error}
        loading={s.loading}
        loadMore={loadMore}
        renderCard={(item) => <Card item={item} href={`/items/${encodeURIComponent(item.id)}`} size="poster" />}
      />
      {sort === "title" && (
        <nav className="azrail" aria-label="A–Z">
          {LETTERS.map((l) => (
            <button key={l} type="button" aria-current={query.from === l ? "true" : undefined} onClick={() => setParams({ from: l })}>{l}</button>
          ))}
        </nav>
      )}
    </>
  );
}
