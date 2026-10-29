import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import type { MediaVersion, Resume, UserItemState } from "../src/api/client.ts";
import { formatBytes, formatClock, minutesLeft, resolutionName, resumeChoice, versionLabel } from "../src/browse/format.ts";

const fixture = (name: string) =>
  JSON.parse(readFileSync(new URL(`../../testdata/contracts/catalogue/${name}`, import.meta.url), "utf8"));
const resumeOf = (positionMs: number) => ({ positionMs, durationMs: 720000 }) as Resume;

test("formatClock", () => {
  assert.equal(formatClock(462000), "7:42");
  assert.equal(formatClock(3723000), "1:02:03");
  assert.equal(formatClock(0), "0:00");
});

test("minutesLeft", () => {
  assert.equal(minutesLeft(resumeOf(480000), 720000), 4);
  assert.equal(minutesLeft(null, 720000), null);
  assert.equal(minutesLeft(resumeOf(600000), null), null); // shared-file episode: no duration
});

test("formatBytes", () => {
  assert.equal(formatBytes(1181116006), "1.1 GB");
  assert.equal(formatBytes(512), "512 B");
  assert.equal(formatBytes(5 * 1024 * 1024), "5 MB");
});

test("versionLabel", () => {
  const viewer = fixture("item-movie.json").versions;
  const admin = fixture("item-movie-admin.json").versions;
  assert.equal(versionLabel(viewer[0]), "1080p · H.264 · 1.1 GB");
  assert.equal(versionLabel(viewer[1]), "Director's Cut · 4K · HEVC · 22.4 GB"); // viewer: no file name
  assert.equal(versionLabel(admin[0]), "1080p · H.264 · 1.1 GB (synthetic.movie.1080p.mp4)");
  assert.equal(versionLabel(admin[1]), "Director's Cut · 4K · HEVC · 22.4 GB (synthetic.directors.cut.2160p.mkv)");
});

test("resolutionName", () => {
  const v = (width: number, height: number) => ({ video: [{ width, height }] }) as never;
  assert.equal(resolutionName(v(3840, 2160)), "4K");
  assert.equal(resolutionName(v(1920, 800)), "1080p"); // scope crop
  assert.equal(resolutionName(v(1280, 720)), "720p");
  assert.equal(resolutionName(v(720, 480)), "SD");
  assert.equal(resolutionName({ video: [] } as never), null);
});

test("resumeChoice", () => {
  const [v1, v2] = fixture("item-movie.json").versions as MediaVersion[];
  const stateAt = (positionMs: number, versionId: string, sourceRevision: string) =>
    ({ resume: { positionMs, durationMs: 1, versionId, sourceRevision, updatedAt: "" } }) as UserItemState;
  assert.deepEqual(resumeChoice(null, v1), { kind: "none" });
  assert.deepEqual(resumeChoice({ resume: null } as UserItemState, v1), { kind: "none" });
  assert.deepEqual(resumeChoice(stateAt(462000, v1.id, v1.sourceRevision), v1), { kind: "resume", positionMs: 462000 });
  assert.deepEqual(resumeChoice(stateAt(462000, v2.id, v2.sourceRevision), v1), { kind: "confirm", positionMs: 462000 });
  assert.deepEqual(resumeChoice(stateAt(462000, v1.id, "f".repeat(64)), v1), { kind: "confirm", positionMs: 462000 });
  assert.deepEqual(resumeChoice(stateAt(v1.durationMs! + 1, v1.id, v1.sourceRevision), v1), { kind: "confirm", positionMs: v1.durationMs! + 1 });
});
