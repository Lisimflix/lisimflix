import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import Ajv2020 from "@redocly/ajv/dist/2020.js";

const readJSON = (path) => JSON.parse(readFileSync(new URL(path, import.meta.url), "utf8"));
const contract = readJSON("../../docs/contracts/m1-openapi.json");
const ajv = new Ajv2020({ allErrors: true });
// OpenAPI stores schemas under components; Go decoding checks the int64 range.
ajv.addKeyword("components");
ajv.addFormat("int64", true);
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

const invalidFixtures = [
  ["transcoded route", "playback.json", (data) => { data.route = "transcode"; }],
  ["external content URL", "playback.json", (data) => { data.contentUrl = "https://example.invalid/private"; }],
  ["wrong heartbeat interval", "playback.json", (data) => { data.heartbeatIntervalSeconds = 999; }],
  ["wrong idle timeout", "playback.json", (data) => { data.idleTimeoutSeconds = 999; }],
  ["missing field", "playback.json", (data) => { delete data.movieId; }],
  ["unexpected field", "playback.json", (data) => { data.sourcePath = "/synthetic/movie.mp4"; }],
  ["zero duration", "movies.json", (data) => { data.items[0].durationMs = 0; }],
  ["fractional duration", "movies.json", (data) => { data.items[0].durationMs = 1.5; }],
];

for (const [name, file, mutate] of invalidFixtures) {
  test(`rejects ${name}`, () => {
    const data = readJSON(`../../testdata/contracts/m1/${file}`);
    mutate(data);
    const validate = ajv.getSchema(`m1#/components/schemas/${fixtures[file]}`);
    assert.equal(validate(data), false, `${name} was accepted`);
  });
}
