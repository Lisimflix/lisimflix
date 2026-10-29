import { createRoot } from "react-dom/client";
import "@fontsource-variable/inter";
import "@fontsource-variable/outfit";
import { ApiError, createClient } from "./api/client";
import { App } from "./app/App";
import { M1App } from "./player/M1App";
import "./styles.css";

const client = createClient();
const root = createRoot(document.getElementById("root")!);

// shortcut: M1 fallback until LIB-2c registers listLibraries; delete M1App, /movies and Movie in LIB-2c's PR
// Only a 404 not_found (route not registered) means M1; anything else is the browse app.
client.libraries().then(
  () => true,
  (err) => !(err instanceof ApiError && err.status === 404 && err.code === "not_found"),
).then((browse) => {
  // No StrictMode: its development double-mount would stop the live playback session.
  root.render(browse ? <App client={client} /> : <M1App client={client} />);
});
