import { useEffect, useRef, useState } from "react";
import { ApiError, type Client, type PlaybackSession, type ProgressEvent } from "../api/client";
import { navigate } from "../app/router";
import { setNotice } from "../app/notice";
import { useStarter } from "./start";
import { VideoPlayer } from "./VideoPlayer";

type State = { name: "preparing" } | { name: "playing"; session: PlaybackSession } | { name: "failed"; message: string };

const CHANGED = "This file changed. Press Play to start again.";

export function PlayerPage({ client, itemId, versionId, startMs, onStop }: {
  client: Client;
  itemId: string;
  versionId: string;
  startMs: number | null;
  onStop: (request: Promise<unknown>) => void;
}) {
  const [state, setState] = useState<State>({ name: "preparing" });
  const [tries, setTries] = useState(0);
  const { generation, start } = useStarter(client);
  // Once we've left, a late answer (e.g. to the final stop report) must not navigate again.
  const gone = useRef(false);
  const detail = `/items/${encodeURIComponent(itemId)}`;

  // Back to the detail page, which refetches the item; kind picks what it shows.
  const leave = (kind: "unavailable" | "changed" | "error", message: string) => {
    if (gone.current) return;
    gone.current = true;
    setNotice({ itemId, versionId, kind, message });
    navigate(detail, { replace: true });
  };

  useEffect(() => {
    gone.current = false;
    setState({ name: "preparing" });
    start(itemId, versionId).then(
      (session) => session && setState({ name: "playing", session }),
      (err: unknown) => {
        const e = err instanceof ApiError ? err : new ApiError(0, "network_error", "Something went wrong.");
        if (e.code === "source_unavailable") leave("unavailable", e.message);
        else if (e.code === "source_changed") leave("changed", CHANGED);
        else setState({ name: "failed", message: e.message });
      },
    );
    // A superseded or unmounted start drops its answer (and stops a late session).
    return () => { ++generation.current; gone.current = true; };
  }, [client, itemId, versionId, tries]);

  // Progress is best effort: 404 (DATA-1 not live) and the rest are ignored, except an ended sign-in or changed file.
  function onProgress(event: ProgressEvent) {
    if (state.name !== "playing") return;
    return client.progress(state.session.id, event).catch((err: unknown) => {
      if (!(err instanceof ApiError)) return;
      if (err.status === 401) leave("error", "Your sign-in has ended.");
      else if (err.status === 409) leave("changed", CHANGED);
    });
  }

  function back() {
    // depth > 0: the previous entry is in-app (normally the detail page).
    if ((history.state?.depth ?? 0) > 0) history.back();
    else navigate(detail, { replace: true });
  }

  return (
    <section className="playerpage">
      <button type="button" className="back" onClick={back}>← Back</button>
      {state.name === "preparing" && <p aria-busy="true">Preparing…</p>}
      {state.name === "failed" && (
        <div className="errorstate">
          <p role="alert">{state.message}</p>
          <button type="button" onClick={() => setTries(tries + 1)}>Try again</button>
        </div>
      )}
      {state.name === "playing" && (
        <VideoPlayer
          client={client}
          session={state.session}
          startMs={startMs ?? undefined}
          onProgress={onProgress}
          onStop={onStop}
          onEnd={(error) => (error ? leave("error", error) : navigate(detail, { replace: true }))}
        />
      )}
    </section>
  );
}
