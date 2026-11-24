import assert from "node:assert/strict";
import { test } from "node:test";
// Node runs this file as it is, so the import names the file.
import {
  limits,
  matches,
  modelState,
  type Provider,
  providerChoices,
  providerSummary,
  sibling,
  takesImages,
  thinkingNote,
  toolModules,
} from "./llm.ts";

const flash = { id: "deepseek-flash", display_name: "DeepSeek-V4.1-Flash", input_modalities: ["text", "image"] };
const pro = { id: "deepseek-v4-pro", input_modalities: ["text"], thinking_levels: ["low", "high", "max"] };
const providers: Provider[] = [
  { name: "deepseek", kind: "openai", fetched_at: "2026-11-24T12:00:00Z", models: [flash, pro] },
  {
    name: "gemini",
    kind: "gemini",
    models: [],
  },
];

test("the field beside another is the one of that name in the same object", () => {
  assert.equal(sibling("model", "provider"), "provider");
  assert.equal(sibling("translate.model", "provider"), "translate.provider");
  assert.equal(sibling("profiles[2].model", "provider"), "profiles[2].provider");
  assert.equal(sibling("steps[0].thinking", "model"), "steps[0].model");
});

test("the providers offered are the configured ones, and a saved name that is not, flagged", () => {
  assert.deepEqual(
    providerChoices(providers, "deepseek").map((c) => [c.name, c.provider?.kind]),
    [
      ["deepseek", "openai"],
      ["gemini", "gemini"],
    ],
  );
  assert.deepEqual(
    providerChoices(providers, "openrouter").map((c) => [c.name, c.provider?.kind]),
    [
      ["deepseek", "openai"],
      ["gemini", "gemini"],
      ["openrouter", undefined],
    ],
  );
  assert.equal(providerChoices(providers, "").length, 2);
});

test("a provider says its kind and how many models were fetched", () => {
  assert.equal(providerSummary(providers[0] as Provider), "2 models · OpenAI-compatible");
  assert.equal(providerSummary(providers[1] as Provider), "No list · Gemini");
  assert.equal(
    providerSummary({ name: "x", kind: "gemini", fetched_at: "2026-11-24T12:00:00Z", models: [{ id: "a" }] }),
    "1 model · Gemini",
  );
});

test("a model's value is in the list fetched, not in it, or there is no list", () => {
  assert.deepEqual(modelState("deepseek-flash", providers[0]), { kind: "listed", model: flash });
  assert.deepEqual(modelState("deepseek-v5", providers[0]), { kind: "unlisted", fetchedAt: "2026-11-24T12:00:00Z" });
  assert.deepEqual(modelState("gemini-3.5-flash", providers[1]), { kind: "no-list" });
  assert.deepEqual(modelState("anything", undefined), { kind: "no-list" });
  assert.deepEqual(modelState("", providers[0]), { kind: "empty" });
});

test("a list fetched empty is a list: a value is not in it", () => {
  const none: Provider = { name: "proxy", kind: "openai", fetched_at: "2026-11-24T12:00:00Z", models: [] };
  assert.deepEqual(modelState("m", none), { kind: "unlisted", fetchedAt: "2026-11-24T12:00:00Z" });
});

test("models are found by ID or display name, in any case", () => {
  assert.ok(matches(flash, "FLASH"));
  assert.ok(matches(flash, "v4.1"));
  assert.ok(!matches(pro, "flash"));
  assert.ok(matches(pro, ""));
});

test("a model's limits read shortly, the context when that is what the list gives", () => {
  assert.equal(limits({ id: "g", input_tokens: 1048576, output_tokens: 65536 }), "1M in · 66K out");
  assert.equal(limits({ id: "d", context_tokens: 1048576, output_tokens: 393216 }), "1M context · 393K out");
  assert.equal(limits({ id: "o" }), "");
});

test("whether a model takes images is known only when its list gives its input kinds", () => {
  assert.equal(takesImages(flash), true);
  assert.equal(takesImages(pro), false);
  assert.equal(takesImages({ id: "g" }), undefined);
});

test("a thinking level the model's list does not give is marked; with no levels listed, none is", () => {
  assert.equal(thinkingNote("low", pro), undefined);
  assert.equal(thinkingNote("medium", pro), "deepseek-v4-pro does not list it");
  assert.equal(thinkingNote("medium", flash), undefined);
  assert.equal(thinkingNote("medium", undefined), undefined);
});

test("toolModules are the modules that offer agents tools, with whether each runs", () => {
  const modules = [
    { id: "echo", running: true, provides: [] },
    { id: "notes", running: false, provides: ["llm.tools/notes"] },
    { id: "media", running: true, provides: ["media.describe", "media.stickers"] },
    { id: "todou", running: true, provides: ["llm.tools/todou"] },
  ];
  assert.deepEqual(toolModules(modules), [
    { id: "notes", running: false },
    { id: "todou", running: true },
  ]);
});
