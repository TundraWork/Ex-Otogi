import assert from "node:assert/strict";
import { test } from "node:test";
// Node runs this file as it is, so the import names the file.
import { exactly, type Person, seenIn, withID } from "./people.ts";

const alice: Person = {
  id: "telegram:user:5",
  name: "Alice",
  last_seen: "2026-11-01T12:00:00Z",
  chats: [
    { id: "telegram:chat:1", name: "Friends", last_seen: "2026-11-01T12:00:00Z" },
    { id: "qq:group:7C2E04D1A9B35F86", last_seen: "2026-10-01T12:00:00Z" },
  ],
};
const alicia: Person = { id: "telegram:user:56", last_seen: "2026-11-01T11:00:00Z", chats: [] };

test("a person was seen in the chats by their names, or a short form of the ID of one without", () => {
  assert.equal(seenIn(alice), "Friends, Group 7C2E04D1…");
  assert.equal(seenIn(alicia), "");
});

test("a person is the one of exactly the ID, not one whose ID only starts with it", () => {
  assert.equal(exactly([alicia, alice], "telegram:user:5"), alice);
  assert.equal(exactly([alicia], "telegram:user:5"), undefined);
  assert.equal(exactly(undefined, "telegram:user:5"), undefined);
  assert.equal(exactly([alice], ""), undefined);
});

test("an ID typed is added once, trimmed, and nothing typed adds nothing", () => {
  assert.deepEqual(withID(["telegram:user:5"], " qq:user:A1 "), ["telegram:user:5", "qq:user:A1"]);
  assert.deepEqual(withID(["telegram:user:5"], "telegram:user:5"), ["telegram:user:5"]);
  assert.deepEqual(withID([], "  "), []);
});
