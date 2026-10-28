// Compile-time contract checks: `npm run typecheck` fails if the generated
// types drift from these synthetic values (testdata/contracts/m1).
import type { components } from "../src/api/schema";

type Movie = components["schemas"]["Movie"];
type PlaybackSession = components["schemas"]["PlaybackSession"];
type AuthState = components["schemas"]["AuthState"];
type UserItemState = components["schemas"]["UserItemState"];
type ProgressEvent = components["schemas"]["ProgressEvent"];
type ProgressResult = components["schemas"]["ProgressResult"];
type ItemSummary = components["schemas"]["ItemSummary"];

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

// CTR-2 (testdata/contracts/catalogue)
export const freshState = {
  itemId: "epi-synthetic-0002",
  revision: 0,
  resume: null,
  watched: null,
  playCount: 0,
  lastPlayedAt: null,
  hiddenFromContinueWatching: false,
} satisfies UserItemState;

export const seek = { sequence: 42, kind: "seek", positionMs: 2400000, fromPositionMs: 1830000 } satisfies ProgressEvent;

export const duplicate = { accepted: false, reason: "duplicate", state: freshState } satisfies ProgressResult;

// @ts-expect-error positions are numbers of milliseconds, not strings
export const badPosition: ProgressEvent = { ...seek, positionMs: "2400000" };

// @ts-expect-error reject reasons are a closed set
export const badReason: ProgressResult = { ...duplicate, reason: "late" };

export const episode = {
  id: "epi-synthetic-0002",
  kind: "episode",
  libraryId: "lib-synthetic-anime",
  parentId: "sea-synthetic-0001",
  title: "Synthetic Episode Two",
  sortTitle: "Synthetic Episode Two",
  year: 2025,
  index: 2,
  durationMs: null,
  contentRating: null,
  primaryImageUrl: null,
  backdropUrl: null,
  userState: freshState,
} satisfies ItemSummary;

// @ts-expect-error item kinds are a closed set
export const badKind: ItemSummary = { ...episode, kind: "collection" };
