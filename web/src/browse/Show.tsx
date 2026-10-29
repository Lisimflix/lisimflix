import { useEffect, useState } from "react";
import { ApiError, type Client, type ItemSummary } from "../api/client";
import { Link, navigate, useSearch } from "../app/router";
import { ErrorState, Skeleton } from "../ui/States";
import { resumeChoice } from "./format";

// A show's children are few enough to read whole (seasons, or one season's episodes).
async function children(client: Client, parentId: string, signal: AbortSignal): Promise<ItemSummary[]> {
  const all: ItemSummary[] = [];
  let cursor: string | undefined;
  do {
    const page = await client.items({ parentId, limit: 100, ...(cursor ? { cursor } : {}) }, signal);
    all.push(...page.items);
    cursor = page.nextCursor ?? undefined;
  } while (cursor);
  return all;
}

type Load<T> = { for: string; items: T } | { for: string; error: ApiError };

function useChildren(client: Client, parentId: string | undefined) {
  const [res, setRes] = useState<Load<ItemSummary[]> | null>(null);
  const [tries, setTries] = useState(0);
  useEffect(() => {
    if (!parentId) return;
    setRes(null);
    const abort = new AbortController();
    children(client, parentId, abort.signal).then(
      (items) => setRes({ for: parentId, items }),
      (e) => !abort.signal.aborted && setRes({ for: parentId, error: e instanceof ApiError ? e : new ApiError(0, "network_error", "Something went wrong.") }),
    );
    return () => abort.abort();
  }, [client, parentId, tries]);
  return [res && res.for === parentId ? res : null, () => setTries(tries + 1)] as const;
}

export function Show({ client, show }: { client: Client; show: ItemSummary }) {
  const wanted = new URLSearchParams(useSearch()).get("season");
  const [seasons, retrySeasons] = useChildren(client, show.id);
  const list = seasons && "items" in seasons ? seasons.items : [];
  const season = list.find((s) => s.id === wanted) ?? list[0];
  const [episodes, retryEpisodes] = useChildren(client, season?.id);
  const [status, setStatus] = useState<string | null>(null);

  // Row Play: the summary has no version, so look the item up and take its default.
  async function play(id: string) {
    setStatus(null);
    try {
      const { item, versions } = await client.item(id);
      const v = versions.find((x) => x.available) ?? versions[0];
      // An offline file gets the detail page's File offline state, never a session.
      if (!v) return setStatus("No file available for this item");
      const choice = resumeChoice(item.userState, v);
      // Offline files and resume points from another version go to the detail page's choice (decision 9).
      if (!v.available || choice.kind === "confirm") navigate(`/items/${encodeURIComponent(id)}`);
      else navigate(`/play/${encodeURIComponent(id)}/${encodeURIComponent(v.id)}${choice.kind === "resume" ? `?t=${choice.positionMs}` : ""}`);
    } catch (e) {
      setStatus(e instanceof ApiError ? e.message : "Something went wrong.");
    }
  }

  if (!seasons) return <Skeleton kind="row" />;
  if ("error" in seasons) return <ErrorState error={seasons.error} thing="season list" level={2} onRetry={retrySeasons} />;
  if (!season) return <p>No seasons yet</p>;
  return (
    <section className="seasons">
      <label>
        Season{" "}
        <select aria-label="Season" value={season.id} onChange={(e) => navigate(`${location.pathname}?season=${encodeURIComponent(e.target.value)}`, { replace: true })}>
          {list.map((s) => <option key={s.id} value={s.id}>{s.title}</option>)}
        </select>
      </label>
      {status && <p role="status">{status}</p>}
      {!episodes ? <Skeleton kind="row" /> : "error" in episodes ? <ErrorState error={episodes.error} thing="episode list" level={2} onRetry={retryEpisodes} /> : (
        <ol className="episodes">
          {episodes.items.map((e) => {
            const resume = e.userState?.resume ?? null;
            const pct = resume && e.durationMs ? Math.min(100, Math.round((resume.positionMs / e.durationMs) * 100)) : null;
            return (
              <li key={e.id}>
                <span className="num">{e.index}</span>
                <Link href={`/items/${encodeURIComponent(e.id)}`}>{e.title}</Link>
                {e.available === false && <span className="offline">File offline</span>}
                {pct !== null && <span className="bar" role="progressbar" aria-label="Progress" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct}><span style={{ width: `${pct}%` }} /></span>}
                <button type="button" onClick={() => play(e.id)}>Play</button>
              </li>
            );
          })}
        </ol>
      )}
    </section>
  );
}
