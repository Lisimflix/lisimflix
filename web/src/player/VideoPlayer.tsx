import { useEffect, useRef, useState } from "react";
import { ApiError, type M1Client, type PlaybackSession } from "../api/client";

interface Props {
  client: M1Client;
  session: PlaybackSession;
  // Called once when playback finishes; error is set when it stopped abnormally.
  onEnd: (error?: string) => void;
  // Called with the stop request, so the app can wait for it (e.g. before logout).
  onStop: (request: Promise<unknown>) => void;
}

export function VideoPlayer({ client, session, onEnd, onStop }: Props) {
  const video = useRef<HTMLVideoElement>(null);
  const [blocked, setBlocked] = useState(false);
  const finish = useRef<(error?: string) => void>(() => undefined);

  useEffect(() => {
    const abort = new AbortController();
    let done = false;
    let sequence = 0;
    const stop = () => {
      if (done) return;
      done = true;
      clearInterval(timer);
      abort.abort();
      onStop(client.stop(session.id).catch(() => undefined));
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

    const onPageHide = () => stop();
    window.addEventListener("pagehide", onPageHide);
    return () => {
      window.removeEventListener("pagehide", onPageHide);
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
        onPlaying={() => setBlocked(false)}
        onEnded={() => finish.current()}
        onError={() => finish.current("This movie couldn't be played.")}
      />
      {blocked && (
        <button type="button" onClick={() => void video.current?.play().then(() => setBlocked(false), () => undefined)}>
          Play
        </button>
      )}
    </div>
  );
}
