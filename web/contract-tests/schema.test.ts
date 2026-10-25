// Compile-time contract checks: `npm run typecheck` fails if the generated
// types drift from these synthetic values (testdata/contracts/m1).
import type { components } from "../src/api/schema";

type Movie = components["schemas"]["Movie"];
type PlaybackSession = components["schemas"]["PlaybackSession"];
type AuthState = components["schemas"]["AuthState"];

export const movie = {
  id: "movie-synthetic-0001",
  sourceRevision: "rev-synthetic-0001",
  title: "Synthetic Test Movie",
  durationMs: 5400000,
  container: "mp4",
  videoCodec: "h264",
  width: 1920,
  height: 1080,
  audioCodec: "aac",
} satisfies Movie;

export const session = {
  id: "play-synthetic-0001",
  movieId: "movie-synthetic-0001",
  sourceRevision: "rev-synthetic-0001",
  contentUrl: "/api/v1/playback/sessions/play-synthetic-0001/content?grant=grant-synthetic-0001",
  route: "direct",
  heartbeatIntervalSeconds: 10,
  idleTimeoutSeconds: 60,
} satisfies PlaybackSession;

export const anonymous = {
  authenticated: false,
  viewer: null,
  csrfToken: "csrf-synthetic-anon-0001",
} satisfies AuthState;

// @ts-expect-error durationMs is a number of milliseconds, not a string
export const badDuration: Movie = { ...movie, durationMs: "5400000" };

// @ts-expect-error M1 only has the direct route
export const badRoute: PlaybackSession = { ...session, route: "transcode" };
