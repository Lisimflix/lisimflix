import { useEffect } from "react";
import type { ApiError, Client, Library } from "../api/client";

const SHAPES = { row: 8, grid: 18, detail: 0 } as const;

// Placeholders at the final sizes (same card art/caption heights as the real cards), so content arriving doesn't shift the page.
export function Skeleton({ kind }: { kind: "row" | "grid" | "detail" }) {
  if (kind === "detail") {
    return (
      <div className="skeleton detail-skel" aria-busy="true">
        <span className="poster" />
        <div><span className="line wide" /><span className="line" /><span className="line" /></div>
      </div>
    );
  }
  return (
    <div className={`skeleton ${kind}-skel`} aria-busy="true">
      {Array.from({ length: SHAPES[kind] }, (_, i) => (
        <div key={i} className="card ph" aria-hidden="true"><span className="art" /><span className="cap" /></div>
      ))}
    </div>
  );
}

const TYPE_NOUN: Record<string, string> = { movies: "movies", tv: "shows", anime: "anime" };

// Empty library. While its first scan runs it shows progress and re-polls libraries() every 5 s (stops on unmount).
export function Empty({ library: lib, client, onPoll }: { library: Library; client: Client; onPoll: (next: Library) => void }) {
  const scanning = lib.scan.state === "scanning";
  useEffect(() => {
    if (!scanning) return;
    const abort = new AbortController();
    const id = setInterval(() => {
      client.libraries(abort.signal).then(
        (l) => { const next = l.items.find((x) => x.id === lib.id); if (next) onPoll(next); },
        () => undefined, // next tick tries again
      );
    }, 5000);
    return () => { clearInterval(id); abort.abort(); };
  }, [client, lib.id, scanning, onPoll]);
  const { filesSeen, filesTotal } = lib.scan;
  return (
    <section className="empty" aria-label="Empty library">
      <h2>No {TYPE_NOUN[lib.type] ?? "titles"} yet</h2>
      {scanning && (
        <>
          <p>This library's folder is being scanned for the first time. Titles appear here as they're found.</p>
          <progress value={filesSeen} {...(filesTotal === null ? {} : { max: filesTotal })} aria-label="Scan progress" />
          <p>{filesTotal === null ? `${filesSeen.toLocaleString("en-US")} files found` : `${filesSeen.toLocaleString("en-US")} of ${filesTotal.toLocaleString("en-US")} files`}</p>
        </>
      )}
    </section>
  );
}

export function ErrorState({ error, onRetry, thing }: { error: ApiError; onRetry: () => void; thing: string }) {
  return (
    <div className="errorstate">
      <div role="alert">
        <h3>Couldn't load this {thing}</h3>
        {error.requestId && <p className="diag">Diagnostic ID {error.requestId}</p>}
      </div>
      <button type="button" onClick={onRetry}>Try again</button>
    </div>
  );
}

// A refetch failed with no connection but earlier data is still on screen.
export function OfflineBanner({ onRetry }: { onRetry: () => void }) {
  return (
    <p role="status" className="banner">
      Can't reach the server. Showing what was loaded before. <button type="button" onClick={onRetry}>Try again</button>
    </p>
  );
}

export function OfflineLibrary() {
  return (
    <section className="empty" aria-label="Library offline">
      <h2>This library's folder is offline</h2>
      <p>Titles come back when it's reconnected. Nothing has been removed.</p>
    </section>
  );
}

export function PartialBanner() {
  return <p role="status" className="banner">Some titles are offline</p>;
}

// Detail state for a version whose file can't be read (Task 8 also uses it for 503 source_unavailable from start).
export function SourceUnavailable({ kind }: { kind: "movie" | "episode" }) {
  return (
    <div className="unavailable">
      <h2>File offline</h2>
      <p>The drive holding this {kind} isn't connected. Your progress is kept. Play works again once it's back.</p>
      <button type="button" disabled>Play unavailable</button>
    </div>
  );
}
