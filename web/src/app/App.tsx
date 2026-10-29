import { useEffect, useRef, useState, type FormEvent } from "react";
import { ApiError, type Client, type ItemDetail, type ItemPage, type Library } from "../api/client";
import { cached, clearCache } from "./cache";
import { Link, useRoute, type Route } from "./router";

type View =
  | { name: "loading" }
  | { name: "login"; error?: string }
  | { name: "ready"; username: string; libraries: Library[] }
  | { name: "error"; message: string };

const message = (err: unknown) => (err instanceof ApiError ? err.message : "Something went wrong.");

export function App({ client }: { client: Client }) {
  const [view, setView] = useState<View>({ name: "loading" });

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
      if (state.authenticated && state.viewer) await enter();
      else setView({ name: "login", error: "Sign-in failed." });
    } catch (err) {
      setView({ name: "login", error: message(err) });
    }
  }

  async function onLogout() {
    // Same order as M1App: leave the page (a player would stop here), then logout, then the new CSRF token.
    setView({ name: "loading" });
    clearCache();
    await client.logout().catch(() => undefined);
    await client.session().catch(() => undefined);
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
  return <Shell client={client} libraries={view.libraries} onLogout={onLogout} />;
}

function Shell({ client, libraries, onLogout }: { client: Client; libraries: Library[]; onLogout: () => void }) {
  const route = useRoute();
  const [menu, setMenu] = useState(false);
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
        <div className="profile">
          <button type="button" aria-expanded={menu} onClick={() => setMenu(!menu)} onKeyDown={(e) => e.key === "Escape" && setMenu(false)}>
            Profile
          </button>
          {menu && (
            <div className="menu">
              <button type="button" autoFocus onClick={onLogout} onKeyDown={(e) => e.key === "Escape" && setMenu(false)}>
                Sign out
              </button>
            </div>
          )}
        </div>
      </header>
      <main>
        <Page client={client} libraries={libraries} route={route} />
      </main>
    </>
  );
}

function Page({ client, libraries, route }: { client: Client; libraries: Library[]; route: Route }) {
  switch (route.name) {
    case "home":
      return <Home client={client} libraries={libraries} />;
    case "library":
      return <h1>{libraries.find((l) => l.id === route.id)?.name ?? "Library"}</h1>;
    case "item":
      return <ItemStub client={client} id={route.id} />;
    case "play":
      return <h1>Play</h1>; // Planned: UI-2 PlayerPage (Task 8).
    default:
      return (
        <>
          <h1>Page not found</h1>
          <Link href="/">Home</Link>
        </>
      );
  }
}

// Placeholder: Task 5 replaces it with the rows. It loads one page through the cache so sign-out has something to clear.
function Home({ client, libraries }: { client: Client; libraries: Library[] }) {
  const [page, setPage] = useState<ItemPage | null>(null);
  const first = libraries[0]?.id;
  useEffect(() => {
    if (!first) return;
    let live = true;
    cached(`items:${first}:added`, () => client.items({ libraryId: first, sort: "added", limit: 20 }))
      .then((p) => live && setPage(p))
      .catch(() => undefined);
    return () => { live = false; };
  }, [client, first]);
  return (
    <>
      <h1>Home</h1>
      <ul>{page?.items.map((i) => <li key={i.id}><Link href={`/items/${encodeURIComponent(i.id)}`}>{i.title}</Link></li>)}</ul>
    </>
  );
}

// Placeholder: Task 6 replaces it with the detail page.
function ItemStub({ client, id }: { client: Client; id: string }) {
  const [item, setItem] = useState<ItemDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const ref = useRef(id);
  useEffect(() => {
    ref.current = id;
    setItem(null);
    setError(null);
    const abort = new AbortController();
    client.item(id, abort.signal).then(setItem, (e) => !abort.signal.aborted && setError(message(e)));
    return () => abort.abort();
  }, [client, id]);
  if (error) return <p role="alert">{error}</p>;
  return <h1>{item ? item.item.title : "Loading…"}</h1>;
}
