import { useEffect, useRef, useState, type FormEvent } from "react";
import { flushSync } from "react-dom";
import { ApiError, type M1Client, type Movie, type PlaybackSession } from "../api/client";
import { useStarter } from "./start";
import { VideoPlayer } from "./VideoPlayer";

type View =
  | { name: "loading" }
  | { name: "login"; error?: string }
  | { name: "ready"; username: string; movie: Movie | null; error?: string }
  | { name: "preparing"; username: string; movie: Movie }
  | { name: "playing"; username: string; movie: Movie; session: PlaybackSession };

function message(err: unknown): string {
  return err instanceof ApiError ? err.message : "Something went wrong.";
}

export function M1App({ client }: { client: M1Client }) {
  const [view, setView] = useState<View>({ name: "loading" });
  const { generation, start } = useStarter(client);
  // The player's last stop request; logout waits for it so the stop isn't sent after the session ends.
  const stopping = useRef<Promise<unknown>>(Promise.resolve());

  async function loadMovies(username: string, signal?: AbortSignal) {
    const gen = ++generation.current;
    try {
      const movies = await client.movies(signal);
      if (gen === generation.current) setView({ name: "ready", username, movie: movies[0] ?? null });
    } catch (err) {
      if (signal?.aborted || gen !== generation.current) return;
      if (err instanceof ApiError && err.status === 401) setView({ name: "login" });
      else setView({ name: "ready", username, movie: null, error: message(err) });
    }
  }

  useEffect(() => {
    const abort = new AbortController();
    client
      .session(abort.signal)
      .then((state) => {
        if (state.authenticated && state.viewer) return loadMovies(state.viewer.username, abort.signal);
        setView({ name: "login" });
      })
      .catch((err) => {
        if (!abort.signal.aborted) setView({ name: "login", error: message(err) });
      });
    return () => abort.abort();
  }, [client]);

  async function onLogin(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    try {
      const state = await client.login(String(form.get("username")), String(form.get("password")));
      if (state.authenticated && state.viewer) await loadMovies(state.viewer.username);
      else setView({ name: "login", error: "Sign-in failed." });
    } catch (err) {
      setView({ name: "login", error: message(err) });
    }
  }

  async function onPlay(username: string, movie: Movie) {
    setView({ name: "preparing", username, movie });
    try {
      const session = await start(movie.id, movie.id);
      if (session) setView({ name: "playing", username, movie, session });
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) setView({ name: "login" });
      else setView({ name: "ready", username, movie, error: message(err) });
    }
  }

  async function onLogout() {
    ++generation.current;
    // Unmount the player now, so its stop goes out before the logout.
    flushSync(() => setView({ name: "loading" }));
    // Don't hang on Loading if the stop never answers; logout revokes playback anyway (#43).
    await Promise.race([stopping.current, new Promise((r) => setTimeout(r, 2000))]);
    await client.logout().catch(() => undefined);
    // Logout rotates the CSRF token; fetch the anonymous one before showing the form.
    await client.session().catch(() => undefined);
    setView({ name: "login" });
  }

  if (view.name === "loading") return <main aria-busy="true"><p>Loading…</p></main>;

  if (view.name === "login") {
    return (
      <main>
        <h1>Lisimflix</h1>
        <form onSubmit={onLogin} aria-label="Sign in">
          <label>
            Username
            <input name="username" autoComplete="username" required maxLength={64} />
          </label>
          <label>
            Password
            <input name="password" type="password" autoComplete="current-password" required maxLength={1024} />
          </label>
          <button type="submit">Sign in</button>
          {view.error && <p role="alert">{view.error}</p>}
        </form>
      </main>
    );
  }

  const header = (
    <header>
      <span>Signed in as {view.username}</span>
      <button type="button" onClick={onLogout}>Sign out</button>
    </header>
  );

  if (view.name === "playing") {
    return (
      <main>
        {header}
        <h1>{view.movie.title}</h1>
        <VideoPlayer
          client={client}
          session={view.session}
          onStop={(request) => { stopping.current = request; }}
          onEnd={(error) => {
            ++generation.current;
            setView({ name: "ready", username: view.username, movie: view.movie, error });
          }}
        />
      </main>
    );
  }

  const movie = view.movie;
  return (
    <main>
      {header}
      {movie ? (
        <>
          <h1>{movie.title}</h1>
          <button type="button" disabled={view.name === "preparing"} onClick={() => onPlay(view.username, movie)}>
            {view.name === "preparing" ? "Preparing…" : "Play"}
          </button>
        </>
      ) : (
        <p>No movie is configured.</p>
      )}
      {view.name === "ready" && view.error && <p role="alert">{view.error}</p>}
    </main>
  );
}
