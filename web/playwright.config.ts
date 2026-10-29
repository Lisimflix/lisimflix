import { defineConfig } from "@playwright/test";

// Tests run against the production bundle (vite preview), so they prove what ships.
export default defineConfig({
  testDir: "tests",
  testMatch: "**/*.spec.ts", // *.test.ts files are node:test unit tests
  fullyParallel: true,
  use: { baseURL: "http://127.0.0.1:5173" },
  projects: [{ name: "chrome", use: { browserName: "chromium", channel: "chrome" } }],
  webServer: {
    command: "npm run build && npx vite preview --host 127.0.0.1 --port 5173 --strictPort",
    url: "http://127.0.0.1:5173",
    reuseExistingServer: false,
  },
});
