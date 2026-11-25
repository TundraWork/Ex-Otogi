import assert from "node:assert/strict";
import { test } from "node:test";
// Node runs this file as it is, so the import names the file.
import { listed, type PeopleList, peopleLists, personField, withPerson } from "./people-lists.ts";
import type { JSONSchema } from "./schema.ts";

// As the server gives chat's settings and the general section's: a list of
// objects naming a person in a field, and a list of people.
const chat: JSONSchema = {
  type: "object",
  properties: {
    profiles: { type: "array", items: { $ref: "#/$defs/Profile" } },
    people: { type: ["array", "null"], items: { $ref: "#/$defs/Person" } },
    timeout: { type: "string" },
  },
  $defs: {
    Profile: {
      type: "object",
      properties: {
        name: { type: "string" },
        // A list inside an item is not one: only those at the top are.
        owners: { type: "array", items: { type: "string", format: "actor" } },
      },
    },
    Person: {
      type: "object",
      "x-order": ["user", "name", "note", "threshold_factor"],
      properties: {
        user: { type: "string", format: "actor" },
        name: { type: "string" },
        note: { type: "string", format: "text" },
        threshold_factor: { type: "number" },
      },
    },
  },
};
const general: JSONSchema = {
  type: "object",
  properties: {
    operators: { type: ["array", "null"], items: { type: "string", format: "actor" } },
    command_prefixes: { type: "array", items: { type: "string" } },
  },
};

/** only is the one list naming people in a schema's settings. */
function only(root: JSONSchema): PeopleList {
  const [list, ...more] = peopleLists(root);
  assert.ok(list);
  assert.equal(more.length, 0);
  return list;
}

test("the lists naming people are those at the top whose items are a person or name one", () => {
  assert.deepEqual(
    peopleLists(chat).map((l) => [l.key, l.person]),
    [["people", "user"]],
  );
  assert.deepEqual(
    peopleLists(general).map((l) => [l.key, l.person]),
    [["operators", ""]],
  );
  assert.deepEqual(peopleLists({ type: "object", properties: { timeout: { type: "string" } } }), []);
});

test("a person is listed by their ID, with the item's own name, never their own again", () => {
  const people = only(chat);
  const settings = {
    people: [
      { user: "qq:user:A1", name: "小林", note: "the owner" },
      { user: "qq:user:B2", threshold_factor: 0.9 },
    ],
  };
  assert.deepEqual(listed(people, settings, "qq:user:A1"), { index: 0, name: "小林" });
  // Without a name of its own, the item is called by the person: no name to add.
  assert.deepEqual(listed(people, settings, "qq:user:B2"), { index: 1, name: undefined });
  assert.equal(listed(people, settings, "qq:user:C3"), undefined);
  assert.equal(listed(people, {}, "qq:user:A1"), undefined);

  const operators = only(general);
  assert.deepEqual(listed(operators, { operators: ["qq:user:A1"] }, "qq:user:A1"), { index: 0, name: undefined });
});

test("a person is added once: a new item naming them, or their ID; one listed already stays as it is", () => {
  const people = only(chat);
  const one = [{ user: "qq:user:A1", name: "小林" }];
  assert.deepEqual(withPerson(people, one, "qq:user:B2"), {
    items: [...one, { user: "qq:user:B2" }],
    at: 1,
    added: true,
  });
  assert.deepEqual(withPerson(people, one, "qq:user:A1"), { items: one, at: 0, added: false });
  assert.deepEqual(withPerson(people, undefined, "qq:user:A1"), {
    items: [{ user: "qq:user:A1" }],
    at: 0,
    added: true,
  });

  const operators = only(general);
  assert.deepEqual(withPerson(operators, ["qq:user:A1"], "qq:user:B2"), {
    items: ["qq:user:A1", "qq:user:B2"],
    at: 1,
    added: true,
  });
});

test("the field to fill after the person is the first other one of an item; a list of people has none", () => {
  const people = only(chat);
  assert.equal(personField(people), "name");
  const operators = only(general);
  assert.equal(personField(operators), undefined);
});
