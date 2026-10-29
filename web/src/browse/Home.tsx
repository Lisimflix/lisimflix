import { useEffect, useState } from "react";
import { ApiError, type Client, type ItemDetail, type ItemPage, type ItemSummary, type Library } from "../api/client";
import { cached } from "../app/cache";
import { Link, navigate } from "../app/router";
import { Card } from "../ui/Card";
import { Chip } from "../ui/Chips";
import { Row } from "../ui/Row";
import { codecName, resolutionName } from "./format";

type Load = { status: "loading" } | { status: "ok"; items: ItemSummary[] } | { status: "missing" } | { status: "error" };

// One home row's data, through the cache so sign-out can clear it. A 404 not_found means the operation isn't live yet.
function useRow(key: string, load: () => Promise<ItemPage>): [Load, () => void] {
  const [tries, setTries] = useState(0);
  const [state, setState] = useState<Load>({ status: "loading" });
  useEffect(() => {
    let live = true;
    setState({ status: "loading" });
    cached(key, load).then(
      (p) => live && setState({ status: "ok", items: p.items }),
      (e) => live && setState(e instanceof ApiError && e.status === 404 && e.code === "not_found" ? { status: "missing" } : { status: "error" }),
    );
    return () => { live = false; };
  }, [key, tries]);
  return [state, () => setTries(tries + 1)];
}

const watched = (i: ItemSummary) => i.userState?.watched?.value === true;
const itemHref = (i: ItemSummary) => `/items/${encodeURIComponent(i.id)}`;

function HomeRow({ title, state, retry, seeAllHref, unwatched, mixed }: { title: string; state: Load; retry: () => void; seeAllHref?: string; unwatched: boolean; mixed: boolean }) {
  if (state.status === "missing") return null;
  if (state.status === "loading") return <section className="row" aria-label={title}><h2>{title}</h2><p>Loading…</p></section>;
  if (state.status === "error") {
    return (
      <section className="row" aria-label={title}>
        <h2>{title}</h2>
        <p role="alert">Couldn't load this row.</p>
        <button type="button" onClick={retry}>Try again</button>
      </section>
    );
  }
  const items = unwatched ? state.items.filter((i) => !watched(i)) : state.items;
  if (items.length === 0) return null;
  return (
    <Row
      title={title}
      seeAllHref={seeAllHref}
      items={items}
      renderCard={(i) => <Card key={i.id} item={i} href={itemHref(i)} size={mixed ? "wide" : "poster"} showTitle={mixed} />}
    />
  );
}

function UpNext({ client, first }: { client: Client; first: ItemSummary }) {
  const [detail, setDetail] = useState<ItemDetail | null>(null);
  useEffect(() => {
    let live = true;
    cached(`item:${first.id}`, () => client.item(first.id)).then((d) => live && setDetail(d), () => undefined);
    return () => { live = false; };
  }, [client, first.id]);
  const resume = first.userState?.resume ?? null;
  const version = detail?.versions.find((v) => v.id === resume?.versionId) ?? detail?.versions[0];
  const chips = [
    first.year === null ? null : String(first.year),
    first.contentRating?.value,
    detail?.genres[0],
    version ? resolutionName(version) : null,
    version?.video[0] ? codecName(version.video[0].codec) : null,
  ].filter((c): c is string => !!c);
  const sub = first.kind === "episode" && first.showTitle ? `${first.showTitle} · S${first.seasonIndex} E${first.index}` : null;
  return (
    <section className="upnext" aria-label="Up next" style={first.backdropUrl ? { backgroundImage: `linear-gradient(90deg, var(--ground), transparent), url(${first.backdropUrl})` } : undefined}>
      <p className="eyebrow">Up next</p>
      <h2>{first.title}</h2>
      {sub && <p>{sub}</p>}
      {chips.length > 0 && <p className="meta">{chips.join(" · ")}</p>}
      <div className="actions">
        {/* Planned: UI-2 (Task 8) wires real resume; until then it opens the play route at the resume point. */}
        <button type="button" onClick={() => detail && version && navigate(`/play/${encodeURIComponent(first.id)}/${encodeURIComponent(version.id)}${resume ? `?t=${resume.positionMs}` : ""}`)} disabled={!detail}>
          Resume
        </button>
        <Link href={itemHref(first)} className="button">Details</Link>
      </div>
    </section>
  );
}

const FILTERS = [
  { label: "Movies", type: "movies" },
  { label: "TV", type: "tv" },
  { label: "Anime", type: "anime" },
] as const;

export function Home({ client, libraries }: { client: Client; libraries: Library[] }) {
  const [type, setType] = useState<string | null>(null);
  const [unwatched, setUnwatched] = useState(false);
  const [cw, retryCw] = useRow("continue-watching", () => client.continueWatching());
  const [nu, retryNu] = useRow("next-up", () => client.nextUp());
  const first = cw.status === "ok" ? cw.items[0] : undefined;
  return (
    <>
      <h1 className="sr">Home</h1>
      <div className="chips" role="group" aria-label="Filter rows">
        <Chip label="All" pressed={type === null && !unwatched} onClick={() => { setType(null); setUnwatched(false); }} />
        {FILTERS.map((f) => (
          <Chip key={f.type} label={f.label} pressed={type === f.type} onClick={() => setType(type === f.type ? null : f.type)} />
        ))}
        <Chip label="Unwatched" pressed={unwatched} onClick={() => setUnwatched(!unwatched)} />
      </div>
      {type === null && first && <UpNext client={client} first={first} />}
      {type === null && <HomeRow title="Continue Watching" state={cw} retry={retryCw} unwatched={unwatched} mixed />}
      {type === null && <HomeRow title="Next Up" state={nu} retry={retryNu} unwatched={unwatched} mixed />}
      {libraries.filter((l) => type === null || l.type === type).map((l) => (
        <LibraryRow key={l.id} client={client} library={l} unwatched={unwatched} />
      ))}
    </>
  );
}

function LibraryRow({ client, library, unwatched }: { client: Client; library: Library; unwatched: boolean }) {
  const [state, retry] = useRow(`items:${library.id}:added`, () => client.items({ libraryId: library.id, sort: "added", limit: 20 }));
  return <HomeRow title={`Recently Added · ${library.name}`} state={state} retry={retry} seeAllHref={`/libraries/${encodeURIComponent(library.id)}?sort=added`} unwatched={unwatched} mixed={false} />;
}
