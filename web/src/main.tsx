import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

// Seed only: UI-1 replaces this with the real player. It must never imply playback works.
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <h1>M1 player not connected</h1>
  </StrictMode>,
);
