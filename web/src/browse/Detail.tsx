import { useEffect, useRef, useState } from "react";
import { ApiError, type Client, type ItemDetail, type MediaVersion } from "../api/client";
import { ErrorState, SourceUnavailable, Skeleton } from "../ui/States";
import { invalidate } from "../app/cache";
import { takeNotice, type Notice } from "../app/notice";
import { Link, navigate } from "../app/router";
import { codecName, formatClock, resolutionName, resumeChoice, versionLabel } from "./format";
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
  // From the player page we just left (File offline, changed file); read once.
  const [notice] = useState(() => takeNotice(id));
  const [error, setError] = useState<ApiError | null>(null);
  const [tries, setTries] = useState(0);
  useEffect(() => {
    setDetail(null);
    setError(null);
    const abort = new AbortController();
    client.item(id, abort.signal).then(setDetail, (e) => !abort.signal.aborted && setError(e instanceof ApiError ? e : new ApiError(0, "network_error", "Something went wrong.")));
    return () => abort.abort();
  }, [client, id, tries]);
  if (error) return <ErrorState error={error} thing="item" onRetry={() => setTries(tries + 1)} />;
  if (!detail) return <Skeleton kind="detail" />;
  return <DetailView key={id} client={client} detail={detail} notice={notice} refetch={() => client.item(id).then(setDetail)} />;
}

function DetailView({ client, detail, notice, refetch }: { client: Client; detail: ItemDetail; notice: Notice | null; refetch: () => Promise<void> }) {
  const { item } = detail;
  // Start said the file is offline although the detail we loaded said otherwise: show it offline.
  const versions = detail.versions.map((v) => (notice?.kind === "unavailable" && v.id === notice.versionId ? { ...v, available: false } : v));
  const [versionId, setVersionId] = useState((notice && versions.find((v) => v.id === notice.versionId)) ? notice.versionId : defaultVersion(versions)?.id);
  const confirm = useRef<HTMLDialogElement>(null);
  const [state, setState] = useState(item.userState);
  const [status, setStatus] = useState<string | null>(null);
  useEffect(() => setState(item.userState), [item]);
  const version = versions.find((v) => v.id === versionId);
  const playable = item.kind === "movie" || item.kind === "episode";
  const watched = state?.watched?.value === true;
  const choice = version ? resumeChoice(state, version) : { kind: "none" as const };
  const playHref = (t?: number) => `/play/${encodeURIComponent(item.id)}/${encodeURIComponent(version?.id ?? "")}${t === undefined ? "" : `?t=${t}`}`;

  function back() {
    // depth > 0: the previous entry is in-app. A deep link or reload has none, so go to the item's library.
    if ((history.state?.depth ?? 0) > 0) history.back();
    else navigate(`/libraries/${encodeURIComponent(item.libraryId)}`, { replace: true });
  }

  async function toggleWatched() {
    setStatus(null);
    try {
      setState(await client.setWatched(item.id, !watched, state?.revision ?? 0));
      invalidate();
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
              {version?.available && choice.kind === "none" && <button type="button" onClick={() => navigate(playHref())}>Play</button>}
              {version?.available && choice.kind !== "none" && (
                <>
                  <button type="button" onClick={() => (choice.kind === "resume" ? navigate(playHref(choice.positionMs)) : confirm.current?.showModal())}>
                    Resume from {formatClock(choice.positionMs)}
                  </button>
                  <button type="button" onClick={() => navigate(playHref())}>Play from start</button>
                </>
              )}
              <button type="button" aria-pressed={watched} onClick={toggleWatched}>Mark watched ✓</button>
            </div>
          )}
          {playable && version && !version.available && <SourceUnavailable kind={item.kind as "movie" | "episode"} />}
          {status && <p role="status">{status}</p>}
          {notice && notice.kind !== "unavailable" && <p role="alert">{notice.message}</p>}
          {choice.kind === "confirm" && (
            // Escape closes a modal dialog and returns focus to the button that opened it.
            <dialog ref={confirm} aria-label="Resume from another version">
              <p>Your resume point is from another version. Resume at {formatClock(choice.positionMs)} anyway?</p>
              <button type="button" onClick={() => navigate(playHref(choice.positionMs))}>Resume anyway</button>
              <button type="button" onClick={() => navigate(playHref())}>Play from start</button>
            </dialog>
          )}
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
