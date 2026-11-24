import assert from "node:assert/strict";
import { test } from "node:test";
// Node runs this file as it is, so the import names the file.
import { milliseconds, reading, shorter } from "./duration.ts";

test("a duration is the sum of its parts", () => {
  assert.equal(milliseconds("720h"), 720 * 3_600_000);
  assert.equal(milliseconds("90m"), 5_400_000);
  assert.equal(milliseconds("1h30m"), 5_400_000);
  assert.equal(milliseconds("1.5h"), 5_400_000);
  assert.equal(milliseconds("20s"), 20_000);
  assert.equal(milliseconds("250ms"), 250);
  assert.equal(milliseconds("0s"), 0);
});

test("what is not a duration has no length", () => {
  for (const value of [undefined, "", "30", "30 days", "h", "5m ", "-5m", 30]) {
    assert.equal(milliseconds(value), undefined);
  }
});

test("a duration is shorter by its length, not by how it is written", () => {
  assert.equal(shorter("720h", "72h"), true);
  assert.equal(shorter("72h", "90m"), true);
  assert.equal(shorter("2h", "90m"), true);
  assert.equal(shorter("72h", "720h"), false);
  assert.equal(shorter("90m", "1h30m"), false);
  assert.equal(shorter("720h", "720h"), false);
});

test("a duration that cannot be read is not shorter", () => {
  assert.equal(shorter("720h", "soon"), false);
  assert.equal(shorter(undefined, "72h"), false);
});

test("a duration reads in days, hours, minutes, and seconds", () => {
  assert.equal(reading("720h"), "30 days");
  assert.equal(reading("1h30m"), "1 hour 30 minutes");
  assert.equal(reading("90m"), "1 hour 30 minutes");
  assert.equal(reading("1.5h"), "1 hour 30 minutes");
  assert.equal(reading("25h"), "1 day 1 hour");
  assert.equal(reading("90s"), "1 minute 30 seconds");
  assert.equal(reading("5s"), "5 seconds");
  assert.equal(reading("1500ms"), "1 second 500 milliseconds");
  assert.equal(reading("0s"), "0 seconds");
});

test("what is not a duration has no reading", () => {
  assert.equal(reading("soon"), undefined);
  assert.equal(reading(""), undefined);
  assert.equal(reading(undefined), undefined);
});
