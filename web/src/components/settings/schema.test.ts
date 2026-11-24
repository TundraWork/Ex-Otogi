import assert from "node:assert/strict";
import { test } from "node:test";
// Node runs this file as it is, so the import names the file.
import {
  changes,
  check,
  clean,
  itemTitle,
  type JSONSchema,
  label,
  noun,
  pathLabel,
  placeholder,
  removedFrom,
  swapped,
  validate,
} from "./schema.ts";

const root: JSONSchema = {
  type: "object",
  required: ["profiles"],
  "x-order": ["timeout", "max_media_mb", "profiles", "people", "headers", "judge"],
  properties: {
    timeout: { type: "string", pattern: "^[0-9]+(s|m|h)$", patternDescription: "a duration such as 90s" },
    max_media_mb: { type: "integer", minimum: 1 },
    profiles: { type: ["array", "null"], minItems: 1, items: { $ref: "#/$defs/Profile" } },
    people: { type: ["array", "null"], items: { $ref: "#/$defs/Person" } },
    headers: { type: "object", additionalProperties: { type: "string" } },
    judge: { $ref: "#/$defs/Judge" },
  },
  $defs: {
    Profile: {
      type: "object",
      required: ["name", "keywords", "model"],
      "x-order": ["name", "keywords", "model", "context"],
      properties: {
        name: { type: "string", minLength: 1 },
        keywords: { type: ["array", "null"], minItems: 1, items: { type: "string" } },
        model: { type: "string", minLength: 1 },
        context: { type: "integer", minimum: 0 },
      },
    },
    Person: {
      type: "object",
      required: ["user"],
      "x-order": ["user", "name", "factor"],
      properties: {
        user: { type: "string", format: "actor" },
        name: { type: "string" },
        factor: { type: "number", maximum: 10 },
      },
    },
    Judge: { type: "object", required: ["rules"], properties: { rules: { type: "string" } } },
  },
};

const otogi = { name: "otogi", keywords: ["otogi"], model: "flash" };

test("validate accepts settings the schema allows", () => {
  assert.deepEqual(validate(root, { timeout: "4m", profiles: [otogi], people: [{ user: "telegram:user:1" }] }), []);
});

test("validate finds problems in every item, in the order of the form", () => {
  const value = {
    timeout: "soon",
    max_media_mb: 0,
    profiles: [otogi, { name: "", keywords: [], model: "flash", context: 1.5 }],
    people: [{ name: "Tundra", factor: 99 }],
  };
  assert.deepEqual(validate(root, value), [
    { path: "timeout", message: "Should be a duration such as 90s" },
    { path: "max_media_mb", message: "At least 1" },
    { path: "profiles[1].name", message: "Required" },
    { path: "profiles[1].keywords", message: "At least 1" },
    { path: "profiles[1].context", message: "A whole number" },
    { path: "people[0].user", message: "Required" },
    { path: "people[0].factor", message: "At most 10" },
  ]);
});

test("validate requires a required list and its least length", () => {
  assert.deepEqual(validate(root, {}), [{ path: "profiles", message: "Required" }]);
  assert.deepEqual(validate(root, { profiles: [] }), [{ path: "profiles", message: "At least 1" }]);
});

test("validate checks an optional object only when it is set", () => {
  assert.deepEqual(validate(root, { profiles: [otogi] }), []);
  assert.deepEqual(validate(root, { profiles: [otogi], judge: {} }), [{ path: "judge.rules", message: "Required" }]);
});

test("validate checks the entries of a map", () => {
  const s: JSONSchema = {
    type: "object",
    required: ["agents"],
    properties: { agents: { type: "object", minProperties: 1, additionalProperties: { $ref: "#/$defs/Agent" } } },
    $defs: { Agent: { type: "object", required: ["server"], properties: { server: { type: "string" } } } },
  };
  assert.deepEqual(validate(s, { agents: {} }), [{ path: "agents", message: "At least 1" }]);
  assert.deepEqual(validate(s, { agents: { main: {} } }), [{ path: "agents.main.server", message: "Required" }]);
  const words: JSONSchema = { type: "object", properties: { headers: root.properties?.headers as JSONSchema } };
  assert.deepEqual(validate(words, { headers: { a: "" } }), []);
});

test("check holds a number to its choices", () => {
  const s: JSONSchema = { type: "integer", enum: [1, 2, 4] };
  assert.equal(check(s, 2, false), undefined);
  assert.equal(check(s, 3, false), "One of 1, 2, 4");
});

test("clean drops unset optional values but keeps a map's empty entries", () => {
  const value = {
    timeout: "",
    max_media_mb: Number.NaN,
    profiles: [{ ...otogi, context: undefined }],
    headers: { a: "" },
  };
  assert.deepEqual(clean(root, value), { profiles: [otogi], headers: { a: "" } });
});

test("changes names the fields that differ", () => {
  assert.deepEqual(changes({ a: 1, b: { c: "x" } }, { a: 2, b: { c: "x" } }), [{ path: "a", from: 1, to: 2 }]);
  assert.deepEqual(changes({ tags: ["a"] }, { tags: ["a", "b"] }), [{ path: "tags", from: ["a"], to: ["a", "b"] }]);
});

test("changes reports a removed list item once, not every item after it", () => {
  const [a, b, c, d] = [{ n: "a" }, { n: "b" }, { n: "c" }, { n: "d" }];
  assert.deepEqual(changes({ l: [a, b, c, d] }, { l: [a, c, d] }), [{ path: "l[1]", from: b, to: undefined }]);
  assert.deepEqual(changes({ l: [a, c] }, { l: [a, b, c] }), [{ path: "l[1]", from: undefined, to: b }]);
  assert.deepEqual(changes({ l: [a, b, c] }, { l: [a, { n: "B" }, c] }), [{ path: "l[1].n", from: "b", to: "B" }]);
});

test("itemTitle is the item's name, model, or first value", () => {
  const profile = root.$defs?.Profile as JSONSchema;
  const person = root.$defs?.Person as JSONSchema;
  assert.equal(itemTitle(profile, otogi), "otogi");
  assert.equal(itemTitle(profile, { model: "flash", keywords: ["x"] }), "flash");
  assert.equal(itemTitle(person, { user: "telegram:user:1", name: "Tundra" }), "Tundra");
  assert.equal(itemTitle(person, { user: "telegram:user:1" }), "telegram:user:1");
  assert.equal(itemTitle(person, { name: "" }), undefined);
});

test("itemTitle of an item without a name is the name of the person it names, when the bot has seen them", () => {
  const person = root.$defs?.Person as JSONSchema;
  const names = new Map([["telegram:user:1", "Alice"]]);
  assert.equal(itemTitle(person, { user: "telegram:user:1" }, names), "Alice");
  assert.equal(itemTitle(person, { user: "telegram:user:1", name: "Tundra" }, names), "Tundra");
  assert.equal(itemTitle(person, { user: "telegram:user:2" }, names), "telegram:user:2");
});

test("removedFrom moves the paths of the items after a removed one up one, and drops its own", () => {
  const paths = new Set(["profiles[0]", "profiles[1]", "profiles[1].judge", "profiles[3]", "people[2]"]);
  assert.deepEqual([...removedFrom(paths, "profiles", 1)], ["profiles[0]", "profiles[2]", "people[2]"]);
});

test("label reads a key as words, with units in capitals", () => {
  assert.equal(label("max_tool_rounds"), "Max tool rounds");
  assert.equal(label("max_media_mb"), "Max media MB");
});

test("label spells the words a key cannot", () => {
  assert.equal(label("api_key"), "API key");
  assert.equal(label("base_url"), "Base URL");
  assert.equal(label("eventlog_retention"), "Event log retention");
  assert.equal(label("openai"), "OpenAI-compatible services");
  assert.equal(label("command_prefixes"), "Command prefixes");
  assert.equal(label("app_id"), "App ID");
  assert.equal(label("telegram"), "Telegram accounts");
  assert.equal(label("qq"), "QQ accounts");
});

test("noun is one of what a title names", () => {
  assert.equal(noun("Profiles"), "profile");
  assert.equal(noun("Judge steps"), "step");
  assert.equal(noun("OpenAI-compatible services"), "service");
  assert.equal(noun("QQ accounts"), "account");
});

test("a list emptied is a change to a list of nothing, not to the default", () => {
  assert.deepEqual(changes({ command_prefixes: ["/"] }, clean({}, { command_prefixes: [] })), [
    { path: "command_prefixes", from: ["/"], to: [] },
  ]);
  assert.deepEqual(changes({}, { command_prefixes: [] }), [{ path: "command_prefixes", from: undefined, to: [] }]);
});

test("pathLabel reads a path as its labels", () => {
  assert.equal(pathLabel("profiles[1].max_media_mb"), "Profiles 2 › Max media MB");
  assert.equal(pathLabel("telegram[0].app_hash"), "Telegram accounts 1 › App hash");
  assert.equal(pathLabel(""), "Settings");
});

test("changes reports items that only moved as the list's new order, once", () => {
  const [a, b, c] = [{ n: "a" }, { n: "b" }, { n: "c" }];
  assert.deepEqual(changes({ l: [a, b, c] }, { l: [b, a, c] }), [
    { path: "l", from: [a, b, c], to: [b, a, c], moved: true },
  ]);
  // Moved and edited is a change of each item, as before.
  assert.deepEqual(changes({ l: [a, b] }, { l: [{ n: "B" }, a] }), [
    { path: "l[0].n", from: "a", to: "B" },
    { path: "l[1].n", from: "b", to: "a" },
  ]);
});

test("swapped follows paths past two items trading places", () => {
  const paths = new Set(["profiles[0]", "profiles[1].judge", "profiles[2]", "people[1]"]);
  assert.deepEqual([...swapped(paths, "profiles", 0, 1)].sort(), [
    "people[1]",
    "profiles[0].judge",
    "profiles[1]",
    "profiles[2]",
  ]);
});

test("label spells the keys that name a tool or a service", () => {
  assert.equal(label("cli"), "todou CLI");
  assert.equal(label("cli_timeout"), "CLI timeout");
  assert.equal(label("jev"), "TypeSafe Jev");
});

test("placeholder is the default, filled in from the fields beside it where it depends on them", () => {
  assert.equal(placeholder({ type: "integer", default: 20 }, {}, false), "Default 20");
  assert.equal(placeholder({ type: "string" }, {}, false), "");
  assert.equal(placeholder({ type: "integer" }, {}, false), "Default");
  assert.equal(placeholder({ type: "integer" }, {}, true), "");
  assert.equal(placeholder({ type: "string", format: "duration" }, {}, false), "Like 5m");
  const session: JSONSchema = {
    type: "string",
    "x-placeholder": "telegram/{account}/session.json in the data directory",
  };
  assert.equal(
    placeholder(session, { account: "main" }, false),
    "Default telegram/main/session.json in the data directory",
  );
  // Until what it depends on is set, it has none.
  assert.equal(placeholder(session, { account: "" }, false), "");
  assert.equal(placeholder({ type: "string", "x-placeholder": "{server}" }, {}, false), "");
});
