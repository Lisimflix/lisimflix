import { useEffect, useState } from "react";
import { ApiError, type Client, type ItemDetail, type MediaVersion } from "../api/client";
import { Link, navigate } from "../app/router";
import { codecName, resolutionName, versionLabel } from "./format";
import { languageName } from "./Library";
import { Show } from "./Show";

const message = (err: unknown) => (err instanceof ApiError ? err.message : "Something went wrong.");

// The first version that can play; the first of all when none can (Play then reports the offline state, Task 8).
export const defaultVersion = (versions: MediaVersion[]): MediaVersion | undefined => versions.find((v) => v.available) ?? versions[0];

const layout = (channels: number) => (channels === 2 ? "Stereo" : channels === 6 ? "5.1" : channels === 8 ? "7.1" : `${channels}ch`);
const HDR = { sdr: null, hdr10: "HDR10", hdr10plus: "HDR10+", hlg: "HLG", dolbyVision: "Dolby Vision" } as const;

function chips(v: MediaVersion): string[] {
  const video = v.video[0];
  const audio = v.audio.find((a) => a.default) ?? v.audio[0];
  return [
    resolutionName(v),
    video && codecName(video.codec),
    audio && `${audio.profile ?? codecName(audio.codec)} ${layout(audio.channels)}`,
    video && HDR[video.hdr],
    // shortcut: no direct-play field on MediaVersion yet; shown for any playable version until the server reports the play mode
    v.available ? "Direct play" : null,
  ].filter((c): c is string => !!c);
}

const unique = (xs: (string | null)[]) => [...new Set(xs.filter((x): x is string => x !== null).map(languageName))];

export function Detail({ client, id }: { client: Client; id: string }) {
  const [detail, setDetail] = useState<ItemDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    setDetail(null);
    setError(null);
    const abort = new AbortController();
    client.item(id, abort.signal).then(setDetail, (e) => !abort.signal.aborted && setError(message(e)));
    return () => abort.abort();
  }, [client, id]);
  if (error) return <p role="alert">{error}</p>;
  if (!detail) return <p aria-busy="true">Loading…</p>;
  return <DetailView key={id} client={client} detail={detail} refetch={() => client.item(id).then(setDetail)} />;
}

function DetailView({ client, detail, refetch }: { client: Client; detail: ItemDetail; refetch: () => Promise<void> }) {
  const { item, versions } = detail;
  const [versionId, setVersionId] = useState(defaultVersion(versions)?.id);
  const [state, setState] = useState(item.userState);
  const [status, setStatus] = useState<string | null>(null);
  useEffect(() => setState(item.userState), [item]);
  const version = versions.find((v) => v.id === versionId);
  const playable = item.kind === "movie" || item.kind === "episode";
  const watched = state?.watched?.value === true;

  function back() {
    // depth > 0: the previous entry is in-app. A deep link or reload has none, so go to the item's library.
    if ((history.state?.depth ?? 0) > 0) history.back();
    else navigate(`/libraries/${encodeURIComponent(item.libraryId)}`, { replace: true });
  }

  async function toggleWatched() {
    setStatus(null);
    try {
      setState(await client.setWatched(item.id, !watched, state?.revision ?? 0));
    } catch (err) {
      if (err instanceof ApiError && err.code === "revision_conflict") {
        await refetch().catch(() => undefined);
        setStatus("Updated elsewhere, try again");
      } else setStatus(message(err));
    }
  }

  const meta = [
    item.year,
    item.durationMs === null ? null : `${Math.round(item.durationMs / 60000)} min`,
    detail.genres[0],
    detail.communityRating && `${detail.communityRating.source.toUpperCase()} ${detail.communityRating.value}`,
  ].filter((m) => m !== null && m !== undefined && m !== "");
  const side: [string, string[]][] = [
    ["Director", detail.directors],
    ["Studio", detail.studios],
    ["Audio", version ? unique(version.audio.map((a) => a.language)) : []],
    ["Subtitles", version ? unique(version.subtitles.map((s) => s.language)) : []],
    ["Added", [new Date(item.addedAt).toLocaleDateString("en-AU", { day: "numeric", month: "short", year: "numeric", timeZone: "UTC" })]],
  ];

  return (
    <article className="detail">
      {item.backdropUrl && <div className="backdrop" style={{ backgroundImage: `url("${item.backdropUrl}")` }} />}
      <button type="button" className="back" onClick={back}>← Back</button>
      <div className="hero">
        {item.primaryImageUrl && <img className="poster" src={item.primaryImageUrl} alt="" />}
        <div>
          <h1>{item.title}</h1>
          {detail.originalTitle && detail.originalTitle !== item.title && <p className="orig">{detail.originalTitle}</p>}
          {item.kind === "episode" && item.showTitle && <p className="orig">{item.showTitle} · S{item.seasonIndex} E{item.index}</p>}
          <p className="meta">{meta.join(" · ")}</p>
          {playable && version && (
            <ul className="qchips">{chips(version).map((c) => <li key={c} className="chip">{c}</li>)}</ul>
          )}
          {playable && (
            <div className="actions">
              {version && (
                // Planned: UI-2 Task 8 (resume/play buttons replace this placeholder).
                <button type="button" onClick={() => navigate(`/play/${encodeURIComponent(item.id)}/${encodeURIComponent(version.id)}`)}>Play</button>
              )}
              <button type="button" aria-pressed={watched} onClick={toggleWatched}>Mark watched ✓</button>
            </div>
          )}
          {status && <p role="status">{status}</p>}
          {playable && versions.length === 0 && <p>No file available for this item</p>}
        </div>
      </div>
      {detail.overview && <p className="overview">{detail.overview}</p>}
      {versions.length > 1 && (
        <label>
          Version{" "}
          <select aria-label="Version" value={versionId} onChange={(e) => setVersionId(e.target.value)}>
            {versions.map((v) => <option key={v.id} value={v.id}>{versionLabel(v) + (v.available ? "" : " (offline)")}</option>)}
          </select>
        </label>
      )}
      <dl className="side">
        {side.filter(([, v]) => v.length > 0).map(([k, v]) => (
          <div key={k}><dt>{k}</dt><dd>{v.join(", ")}</dd></div>
        ))}
      </dl>
      {item.kind === "show" && <Show client={client} show={item} />}
      {item.kind === "season" && item.parentId && (
        <Link href={`/items/${encodeURIComponent(item.parentId)}?season=${encodeURIComponent(item.id)}`}>{item.showTitle ?? "Show"} · all seasons</Link>
      )}
    </article>
  );
}
