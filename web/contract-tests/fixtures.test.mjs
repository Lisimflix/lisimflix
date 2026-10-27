import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import Ajv2020 from "@redocly/ajv/dist/2020.js";

const readJSON = (path) => JSON.parse(readFileSync(new URL(path, import.meta.url), "utf8"));
const contract = readJSON("../../docs/contracts/openapi.json");
const ajv = new Ajv2020({ allErrors: true });
// OpenAPI stores schemas under components; Go decoding checks the int64 range.
ajv.addKeyword("components");
ajv.addFormat("int64", true);
// RFC 3339 instants with an offset, as Go encodes time.Time; Go decoding checks the calendar.
ajv.addFormat("date-time", /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/);
ajv.addSchema({ components: contract.components }, "m1");

const fixtures = {
  "anonymous.json": "AuthState",
  "authenticated.json": "AuthState",
  "movies.json": "MovieList",
  "playback.json": "PlaybackSession",
  "error.json": "APIError",
};

for (const [file, schema] of Object.entries(fixtures)) {
  test(`${file} satisfies ${schema}`, () => {
    const validate = ajv.getSchema(`m1#/components/schemas/${schema}`);
    const data = readJSON(`../../testdata/contracts/m1/${file}`);
    assert.ok(validate(data), `${file}: ${ajv.errorsText(validate.errors)}`);
  });
}

test("accepts a cropped 1080p movie at its actual size", () => {
  const data = readJSON("../../testdata/contracts/m1/movies.json");
  data.items[0].height = 800;
  assert.ok(ajv.getSchema("m1#/components/schemas/MovieList")(data));
});

test("accepts an empty movie list", () => {
  assert.ok(ajv.getSchema("m1#/components/schemas/MovieList")({ items: [], nextCursor: null }));
});

const invalidFixtures = [
  ["transcoded route", "playback.json", (data) => { data.route = "transcode"; }],
  ["external content URL", "playback.json", (data) => { data.contentUrl = "https://example.invalid/private"; }],
  ["wrong heartbeat interval", "playback.json", (data) => { data.heartbeatIntervalSeconds = 999; }],
  ["wrong idle timeout", "playback.json", (data) => { data.idleTimeoutSeconds = 999; }],
  ["missing field", "playback.json", (data) => { delete data.movieId; }],
  ["unexpected field", "playback.json", (data) => { data.sourcePath = "/synthetic/movie.mp4"; }],
  ["zero duration", "movies.json", (data) => { data.items[0].durationMs = 0; }],
  ["fractional duration", "movies.json", (data) => { data.items[0].durationMs = 1.5; }],
  ["authenticated without viewer", "authenticated.json", (data) => { data.viewer = null; }],
  ["anonymous with viewer", "anonymous.json", (data) => { data.viewer = readJSON("../../testdata/contracts/m1/authenticated.json").viewer; }],
  ["width above 1080p", "movies.json", (data) => { data.items[0].width = 2560; }],
  ["height above 1080p", "movies.json", (data) => { data.items[0].height = 1440; }],
  ["zero height", "movies.json", (data) => { data.items[0].height = 0; }],
];

for (const [name, file, mutate] of invalidFixtures) {
  test(`rejects ${name}`, () => {
    const data = readJSON(`../../testdata/contracts/m1/${file}`);
    mutate(data);
    const validate = ajv.getSchema(`m1#/components/schemas/${fixtures[file]}`);
    assert.equal(validate(data), false, `${name} was accepted`);
  });
}

test("every operation names its owning feature and whether it is implemented", () => {
  for (const [path, item] of Object.entries(contract.paths)) {
    for (const [method, op] of Object.entries(item)) {
      const x = op["x-lisimflix"] ?? {};
      assert.match(x.owner ?? "", /^[A-Z]+-\d+$/, `${method} ${path} owner`);
      assert.ok(["implemented", "planned"].includes(x.status), `${method} ${path} status`);
    }
  }
});

// CTR-2 fixtures (testdata/contracts/catalogue). checkFixtures adds one test
// per fixture and one rejection test per invalid variant.
const catalogue = (file) => readJSON(`../../testdata/contracts/catalogue/${file}`);

function checkFixtures(fixtures, invalid) {
  for (const [file, schema] of Object.entries(fixtures)) {
    test(`catalogue/${file} satisfies ${schema}`, () => {
      const validate = ajv.getSchema(`m1#/components/schemas/${schema}`);
      assert.ok(validate(catalogue(file)), `${file}: ${ajv.errorsText(validate.errors)}`);
    });
  }
  for (const [name, file, mutate] of invalid) {
    test(`rejects ${name}`, () => {
      const data = catalogue(file);
      mutate(data);
      assert.equal(ajv.getSchema(`m1#/components/schemas/${fixtures[file]}`)(data), false, `${name} was accepted`);
    });
  }
}

const userDataFixtures = {
  "progress-seek.json": "ProgressEvent",
  "progress-duplicate.json": "ProgressResult",
  "watched-request.json": "WatchedRequest",
};

checkFixtures(userDataFixtures, [
  ["seek without a from position", "progress-seek.json", (d) => { d.fromPositionMs = null; }],
  ["tick with a from position", "progress-seek.json", (d) => { d.kind = "tick"; }],
  ["negative position", "progress-seek.json", (d) => { d.positionMs = -1; }],
  ["rejected event without a reason", "progress-duplicate.json", (d) => { d.reason = null; }],
  ["accepted event with a reason", "progress-duplicate.json", (d) => { d.accepted = true; }],
  ["unknown reject reason", "progress-duplicate.json", (d) => { d.reason = "late"; }],
  ["resume without a source revision", "progress-duplicate.json", (d) => { delete d.state.resume.sourceRevision; }],
  ["local time without an offset", "progress-duplicate.json", (d) => { d.state.resume.updatedAt = "2026-10-27 08:15:00"; }],
  ["unknown watched origin", "progress-duplicate.json", (d) => { d.state.watched = { value: true, origin: "plex", at: "2026-10-27T08:15:00Z" }; }],
  ["negative play count", "progress-duplicate.json", (d) => { d.state.playCount = -1; }],
  ["fractional expected revision", "watched-request.json", (d) => { d.expectedRevision = 1.5; }],
]);
