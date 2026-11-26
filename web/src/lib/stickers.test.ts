import assert from "node:assert/strict";
import { test } from "node:test";
// Node runs this file as it is, so the import names the file.
import {
  allStickers,
  givenTo,
  inView,
  matches,
  mib,
  moved,
  next,
  noPack,
  onlyIn,
  packIDProblem,
  seenNote,
  sentQuery,
  shownTo,
  size,
  usage,
} from "./stickers.ts";

test("each sticker number names the agents whose settings give it, in order, once each", () => {
  const given = givenTo({
    profiles: [
      { name: "eku", label: "艾可", media: { stickers: [3, 5, 3] } },
      { name: "sol", media: { stickers: [5] } },
      { name: "luna" },
      { name: "", media: { stickers: [9] } },
    ],
  });
  assert.deepEqual(given.get(3), [{ name: "eku", title: "艾可" }]);
  assert.deepEqual(given.get(5), [
    { name: "eku", title: "艾可" },
    { name: "sol", title: "sol" },
  ]);
  assert.equal(given.get(9), undefined);
});

test("packs give their stickers, named by the pack, and a sticker given directly too reads as direct", () => {
  const packs = [
    { id: "eku", stickers: [3, 4] },
    { id: "cats", stickers: [4, 6] },
  ];
  const given = givenTo(
    {
      profiles: [
        { name: "eku", label: "艾可", media: { stickers: [3], packs: ["eku", "cats", "gone"] } },
        { name: "sol", media: { packs: ["cats"] } },
      ],
    },
    packs,
  );
  assert.deepEqual(given.get(3), [{ name: "eku", title: "艾可" }]);
  assert.deepEqual(given.get(4), [
    { name: "eku", title: "艾可", via: "eku" },
    { name: "sol", title: "sol", via: "cats" },
  ]);
  assert.deepEqual(given.get(6), [
    { name: "eku", title: "艾可", via: "cats" },
    { name: "sol", title: "sol", via: "cats" },
  ]);
});

test("settings it cannot read give nothing", () => {
  for (const settings of [
    undefined,
    null,
    {},
    { profiles: "x" },
    { profiles: [{ name: "eku", media: { stickers: "3", packs: "eku" } }] },
  ]) {
    assert.equal(givenTo(settings, [{ id: "eku", stickers: [1] }]).size, 0);
  }
});

// The event log is searched as chat.reply writes its sticker: an object
// whose first field is id. When chat.reply's sticker changes shape, so must
// sentQuery.
test("the search for a sticker's sends matches chat.reply's payload and no other number", () => {
  const payload = JSON.stringify({ sticker: { id: 3, ref: { platform: "qq" } } });
  assert.ok(payload.includes(sentQuery(3)));
  assert.ok(!payload.includes(sentQuery(30)));
  assert.ok(!JSON.stringify({ sticker: { id: 31 } }).includes(sentQuery(3)));
});

test("sizes read as people write them", () => {
  assert.equal(size(100), "1 KiB");
  assert.equal(size(482113), "471 KiB");
  assert.equal(size(3 << 20), "3.0 MiB");
  assert.equal(mib(9.5 * (1 << 20)), "9.5 MiB");
});

test("the card says how many stickers there are and their size", () => {
  assert.equal(usage([{ bytes: 1 << 20 }, { bytes: 1 << 20 }]), "2 · 2.0 MiB");
  assert.equal(usage([]), "0 · 0.0 MiB");
});

test("removing a sticker selects the next, else the one before", () => {
  assert.equal(next([1, 3, 4], 3), 4);
  assert.equal(next([1, 3, 4], 4), 3);
  assert.equal(next([4], 4), undefined);
  assert.equal(next([1, 3], 9), undefined);
});

test("a view shows all stickers, those in no pack, or one pack's", () => {
  assert.ok(inView([], allStickers));
  assert.ok(inView(["eku"], allStickers));
  assert.ok(inView([], noPack));
  assert.ok(!inView(["eku"], noPack));
  assert.ok(inView(["cats", "eku"], "eku"));
  assert.ok(!inView(["cats"], "eku"));
  assert.ok(!inView([], "eku"));
});

test("the filter finds a sticker by its number or by words in its note or summary", () => {
  const s = { id: 12, note: "摸头 安慰", summary: "A cat Smiling" };
  for (const f of ["", " ", "12", "#12", "摸头", "smiling", "CAT"]) {
    assert.ok(matches(s, f), f);
  }
  for (const f of ["1", "#2", "120", "dog"]) {
    assert.ok(!matches(s, f), f);
  }
});

test("a pack ID is checked as the media module checks it", () => {
  assert.equal(packIDProblem("eku-memes", ["cats"]), "");
  assert.equal(packIDProblem("a".repeat(32), []), "");
  assert.equal(packIDProblem("", []), "Give it an ID.");
  assert.equal(packIDProblem("cats", ["cats"]), "A pack with this ID exists.");
  for (const id of ["Eku", "eku pack", "-eku", "eku_memes", "表情", "a".repeat(33)]) {
    assert.match(packIDProblem(id, []), /^Use lowercase letters/, id);
  }
});

test("a pack's stickers in no other pack are counted", () => {
  const stickers = [{ packs: ["eku"] }, { packs: ["cats", "eku"] }, { packs: [] }, { packs: ["eku"] }];
  assert.equal(onlyIn(stickers, "eku"), 2);
  assert.equal(onlyIn(stickers, "cats"), 0);
});

test("a chosen item moves one place earlier or later, and not past either end", () => {
  assert.deepEqual(moved([1, 2, 3], 1, -1), [2, 1, 3]);
  assert.deepEqual(moved([1, 2, 3], 1, 1), [1, 3, 2]);
  assert.deepEqual(moved([1, 2, 3], 0, -1), [1, 2, 3]);
  assert.deepEqual(moved([1, 2, 3], 2, 1), [1, 2, 3]);
  assert.deepEqual(moved([1, 2, 3], 5, 1), [1, 2, 3]);
});

test("an agent's list counts its stickers, then its packs', once each, only those it can see", () => {
  const kept = [
    { id: 1, text: "wave" },
    { id: 2, text: "cry" },
    { id: 3, text: "" },
    { id: 4, text: "laugh" },
    { id: 5, text: "nod" },
  ];
  const packs = [
    { id: "eku", stickers: [2, 3, 4] },
    { id: "misc", stickers: [4, 5] },
  ];
  // 1 and 2 named; eku adds 4 (2 is there, 3 has no text); misc adds 5; 9 is not kept; gone is not there.
  assert.deepEqual(shownTo([1, 2, 9], ["eku", "misc", "gone"], 50, kept, packs), { given: 4, seen: 4 });
  assert.deepEqual(shownTo([1, 2, 9], ["eku", "misc"], 3, kept, packs), { given: 4, seen: 3 });
  assert.deepEqual(shownTo(undefined, ["misc"], 50, kept, packs), { given: 2, seen: 2 });
  assert.deepEqual(shownTo(undefined, undefined, 50, kept, packs), { given: 0, seen: 0 });
});

test("the line under the pickers says how many the agent sees, and when only the first", () => {
  assert.deepEqual(seenNote("艾可", 31, 50), { text: "艾可 sees 31 stickers.", over: false });
  assert.deepEqual(seenNote("艾可", 1, 50), { text: "艾可 sees 1 sticker.", over: false });
  assert.deepEqual(seenNote("艾可", 0, 50), { text: "艾可 sees no stickers.", over: false });
  assert.deepEqual(seenNote("艾可", 62, 50), {
    text: "艾可 is given 62 stickers and sees the first 50: single stickers first, then packs in order.",
    over: true,
  });
});
