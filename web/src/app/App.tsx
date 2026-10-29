import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { flushSync } from "react-dom";
import { ApiError, type Client, type Library } from "../api/client";
import { Detail } from "../browse/Detail";
import { Home } from "../browse/Home";
import { Library as LibraryPage } from "../browse/Library";
import { PlayerPage } from "../player/PlayerPage";
import { clearCache } from "./cache";
import { Link, useRoute, type Route } from "./router";

type View =
  | { name: "loading" }
  | { name: "login"; error?: string }
  | { name: "ready"; username: string; libraries: Library[] }
  | { name: "error"; message: string };

// Catalogue and player calls: a 401 means the session ended, whichever page made the call. The error still reaches the caller.
const GUARDED = ["libraries", "items", "item", "facets", "continueWatching", "nextUp", "setWatched", "progress", "start", "heartbeat", "stop"] as const;

function guard(client: Client, generation: () => number, onExpired: () => void): Client {
  const api = { ...client };
  for (const name of GUARDED) {
    const call = client[name] as (...args: unknown[]) => Promise<unknown>;
    (api as Record<string, unknown>)[name] = (...args: unknown[]) => {
      const started = generation();
      return call(...args).catch((err: unknown) => {
        // A late 401 from an earlier sign-in says nothing about the current one.
        if (err instanceof ApiError && err.status === 401 && started === generation()) onExpired();
        throw err;
      });
    };
  }
  return api;
}

const message = (err: unknown) => (err instanceof ApiError ? err.message : "Something went wrong.");

export function App({ client }: { client: Client }) {
  const [view, setView] = useState<View>({ name: "loading" });
  // The player's last stop request; logout waits for it so the stop isn't sent after the session ends.
  const stopping = useRef<Promise<unknown>>(Promise.resolve());
  const loggingOut = useRef(false);
  const signIns = useRef(0); // bumps whenever the session changes; see guard
  const api = useMemo(() => guard(client, () => signIns.current, () => {
    if (loggingOut.current) return; // logout runs its own order
    signIns.current++;
    clearCache();
    // The CSRF cookie can expire with the session, so fetch a fresh token before the next sign-in (as logout does).
    void client.session().catch(() => undefined).then(() => setView({ name: "login", error: "Your sign-in has ended." }));
  }), [client]);

  async function enter(signal?: AbortSignal) {
    try {
      const state = await client.session(signal);
      if (!state.authenticated || !state.viewer) return setView({ name: "login" });
      const libraries = (await client.libraries(signal)).items;
      setView({ name: "ready", username: state.viewer.username, libraries });
    } catch (err) {
      if (signal?.aborted) return;
      if (err instanceof ApiError && err.status === 401) setView({ name: "login" });
      else setView({ name: "error", message: message(err) });
    }
  }

  useEffect(() => {
    const abort = new AbortController();
    void enter(abort.signal);
    return () => abort.abort();
  }, [client]);

  async function onLogin(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    try {
      const state = await client.login(String(form.get("username")), String(form.get("password")));
      if (state.authenticated && state.viewer) { signIns.current++; await enter(); }
      else setView({ name: "login", error: "Sign-in failed." });
    } catch (err) {
      setView({ name: "login", error: message(err) });
    }
  }

  async function onLogout() {
    // Same order as M1App: unmount now (a player stops here), then logout, then the new CSRF token.
    loggingOut.current = true;
    signIns.current++;
    flushSync(() => setView({ name: "loading" }));
    clearCache();
    // Don't hang on Loading if the stop never answers; logout revokes playback anyway (#43).
    await Promise.race([stopping.current, new Promise((r) => setTimeout(r, 2000))]);
    await client.logout().catch(() => undefined);
    await client.session().catch(() => undefined);
    loggingOut.current = false;
    setView({ name: "login" });
  }

  if (view.name === "loading") return <main aria-busy="true"><p>Loading…</p></main>;
  if (view.name === "error") return <main><p role="alert">{view.message}</p></main>;
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
  return <Shell client={api} libraries={view.libraries} onLogout={onLogout} onStop={(request) => { stopping.current = request; }} />;
}

function Shell({ client, libraries, onLogout, onStop }: { client: Client; libraries: Library[]; onLogout: () => void; onStop: (request: Promise<unknown>) => void }) {
  const route = useRoute();
  const [menu, setMenu] = useState(false);
  const profile = useRef<HTMLDivElement>(null);
  const pathname = location.pathname; // Shell re-renders on every route change
  const search = location.search;
  useEffect(() => setMenu(false), [pathname, search]);
  // After a route change focus would be lost with the old page, so move it to the new h1 (not on first load).
  // Bounded: gives up after 2 s or on the viewer's first key or click, and never takes an h1 the previous route had.
  const first = useRef(true);
  const seen = useRef<Element | null>(null); // the h1 on screen when the route last settled
  useEffect(() => {
    const main = document.querySelector("main")!;
    if (first.current) { first.current = false; seen.current = main.querySelector("h1"); return; }
    const mo = new MutationObserver(() => focusH1() && stop()); // pages that fetch show the h1 later
    const stop = () => {
      mo.disconnect();
      clearTimeout(timer);
      removeEventListener("keydown", stop, true);
      removeEventListener("pointerdown", stop, true);
    };
    const focusH1 = () => {
      const h1 = main.querySelector("h1");
      if (!h1 || h1 === seen.current) return false;
      seen.current = h1;
      // A page may already have restored focus (Back to a grid card).
      if (!main.contains(document.activeElement)) { h1.tabIndex = -1; h1.focus(); }
      return true;
    };
    const timer = setTimeout(stop, 2000);
    addEventListener("keydown", stop, true);
    addEventListener("pointerdown", stop, true);
    if (focusH1()) { stop(); return; }
    mo.observe(main, { childList: true, subtree: true });
    return stop;
  }, [pathname]);
  useEffect(() => {
    if (!menu) return;
    const close = (e: PointerEvent) => !profile.current?.contains(e.target as Node) && setMenu(false);
    document.addEventListener("pointerdown", close);
    return () => document.removeEventListener("pointerdown", close);
  }, [menu]);
  const tabs = [
    { href: "/", label: "Home", current: route.name === "home" },
    ...libraries.map((l) => ({
      href: `/libraries/${encodeURIComponent(l.id)}`,
      label: l.name,
      current: route.name === "library" && route.id === l.id,
    })),
  ];
  return (
    <>
      <header className="shell">
        <Link href="/" className="logo">
          <img src="/logo.svg" alt="Lisimflix" height="28" />
        </Link>
        <nav aria-label="Libraries">
          {tabs.map((t) => (
            <Link key={t.href} href={t.href} aria-current={t.current ? "page" : undefined}>
              {t.label}
            </Link>
          ))}
        </nav>
        <div className="profile" ref={profile} onKeyDown={(e) => {
          if (e.key !== "Escape" || !menu) return;
          setMenu(false);
          profile.current?.querySelector("button")?.focus();
        }}>
          <button type="button" aria-expanded={menu} onClick={() => setMenu(!menu)}>
            Profile
          </button>
          {menu && (
            <div className="menu">
              <button type="button" autoFocus onClick={onLogout}>
                Sign out
              </button>
            </div>
          )}
        </div>
      </header>
      <main>
        <Page client={client} libraries={libraries} route={route} onStop={onStop} />
      </main>
    </>
  );
}

function Page({ client, libraries, route, onStop }: { client: Client; libraries: Library[]; route: Route; onStop: (request: Promise<unknown>) => void }) {
  switch (route.name) {
    case "home":
      return <Home client={client} libraries={libraries} />;
    case "library":
      return <LibraryRoute client={client} libraries={libraries} id={route.id} />;
    case "item":
      return <Detail key={route.id} client={client} id={route.id} />;
    case "play":
      return <PlayerPage key={`${route.itemId}/${route.versionId}`} client={client} itemId={route.itemId} versionId={route.versionId} startMs={route.startMs} onStop={onStop} />;
    default:
      return (
        <>
          <h1>Page not found</h1>
          <Link href="/">Home</Link>
        </>
      );
  }
}

function LibraryRoute({ client, libraries, id }: { client: Client; libraries: Library[]; id: string }) {
  const library = libraries.find((l) => l.id === id);
  // Keyed by library so switching tabs starts a fresh grid.
  return library ? <LibraryPage key={id} client={client} library={library} /> : <h1>Library not found</h1>;
}
