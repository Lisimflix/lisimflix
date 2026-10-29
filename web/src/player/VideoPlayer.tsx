import { useEffect, useRef, useState } from "react";
import { ApiError, type M1Client, type PlaybackSession, type ProgressEvent } from "../api/client";

interface Props {
  client: M1Client;
  session: PlaybackSession;
  // Called once when playback finishes; error is set when it stopped abnormally.
  onEnd: (error?: string) => void;
  // Called with the stop request, so the app can wait for it (e.g. before logout).
  onStop: (request: Promise<unknown>) => void;
  // Where to begin, set once when the metadata is known.
  startMs?: number;
  // The text for a file the browser can't play; the page words it by what the item is.
  mediaError?: () => Promise<string> | string;
  // Progress reports: every 10 s while playing, on pause and seek, and at stop. The stop request waits for the last one.
  onProgress?: (e: ProgressEvent) => Promise<unknown> | void;
}

export function VideoPlayer({ client, session, onEnd, onStop, startMs, onProgress, mediaError = () => "This movie couldn't be played." }: Props) {
  const video = useRef<HTMLVideoElement>(null);
  const [blocked, setBlocked] = useState(false);
  const finish = useRef<(error?: string) => void>(() => undefined);
  // Element events reach the effect's state through this.
  const report = useRef<(kind: "pause" | "seek") => void>(() => undefined);
  const metadata = useRef<() => void>(() => undefined);

  useEffect(() => {
    const abort = new AbortController();
    let done = false;
    let sequence = 0;
    let progressSequence = 0;
    let lastMs = startMs ?? 0;
    let ready = false; // positions mean nothing before the metadata (and the start seek) is in
    let skipSeek = startMs !== undefined && startMs > 0;
    const send = (kind: ProgressEvent["kind"], from: number | null = null) => {
      const positionMs = Math.round(el.currentTime * 1000);
      const request = Promise.resolve(onProgress?.({ sequence: ++progressSequence, kind, positionMs, fromPositionMs: from })).catch(() => undefined);
      lastMs = positionMs;
      return request;
    };
    // leaving: the page is going away, so the report and the DELETE go out together (both keepalive); no waiting for a reply that can't come.
    const stop = (leaving = false) => {
      if (done) return;
      const last = ready ? send("stop") : undefined;
      done = true;
      clearInterval(timer);
      clearInterval(progressTimer);
      abort.abort();
      // The stop waits (up to 1 s, so the DELETE is sent inside logout's 2 s cap, not necessarily finished) for the last report, which a finished session would refuse.
      const wait = last && !leaving ? Promise.race([last, new Promise((r) => setTimeout(r, 1000))]) : undefined;
      onStop(Promise.resolve(wait).then(() => client.stop(session.id)).catch(() => undefined));
    };
    report.current = (kind) => {
      if (done || !ready) return;
      if (kind === "seek") {
        if (skipSeek) { skipSeek = false; return; } // the start position, not the viewer
        void send("seek", lastMs);
      } else void send("pause");
    };
    metadata.current = () => {
      if (done) return;
      if (startMs && startMs / 1000 < el.duration) el.currentTime = startMs / 1000;
      else skipSeek = false;
      ready = true;
    };
    finish.current = (error) => {
      if (done) return;
      stop();
      onEnd(error);
    };

    // The server's content URL is fixed for the whole session; renewals never change src.
    const el = video.current!;
    el.src = session.contentUrl;
    el.play().catch((err: unknown) => {
      if (!done && err instanceof DOMException && err.name === "NotAllowedError") setBlocked(true);
    });

    const timer = setInterval(() => {
      sequence += 1;
      client.heartbeat(session.id, sequence, abort.signal).catch((err: unknown) => {
        // A network blip is retried by the next tick; the server allows six missed beats.
        if (done || !(err instanceof ApiError) || err.status === 0 || err.status >= 500) return;
        finish.current(err.status === 401 ? "Your sign-in has ended." : "Playback has ended. Press Play to start again.");
      });
    }, session.heartbeatIntervalSeconds * 1000);

    const onPageHide = () => stop(true);
    // A bfcache restore brings back this player after pagehide ended its session (#41).
    const onPageShow = (e: PageTransitionEvent) => {
      if (e.persisted) onEnd();
    };
    const progressTimer = setInterval(() => {
      if (!el.paused) void send("tick");
    }, 10_000);
    window.addEventListener("pagehide", onPageHide);
    window.addEventListener("pageshow", onPageShow);
    return () => {
      window.removeEventListener("pagehide", onPageHide);
      window.removeEventListener("pageshow", onPageShow);
      stop();
      el.removeAttribute("src");
      el.load();
    };
  }, [client, session]);

  return (
    <div className="player">
      <video
        ref={video}
        controls
        playsInline
        onLoadedMetadata={() => metadata.current()}
        onPause={() => report.current("pause")}
        onSeeking={() => report.current("seek")}
        onPlaying={() => setBlocked(false)}
        onEnded={() => finish.current()}
        onError={() => void Promise.resolve(mediaError()).then((text) => finish.current(text))}
      />
      {blocked && (
        <button type="button" onClick={() => void video.current?.play().then(() => setBlocked(false), () => undefined)}>
          Play
        </button>
      )}
    </div>
  );
}
