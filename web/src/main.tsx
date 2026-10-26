import { createRoot } from "react-dom/client";
import { createClient } from "./api/client";
import { M1App } from "./player/M1App";
import "./styles.css";

// No StrictMode: its development double-mount would stop the live playback session.
createRoot(document.getElementById("root")!).render(<M1App client={createClient()} />);
