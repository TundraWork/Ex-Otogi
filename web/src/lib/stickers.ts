/** A chat agent given a sticker, as the stickers card names it: its name,
 * the label it shows, if any, and the pack it is given the sticker through,
 * if not directly. */
export type Agent = { name: string; title: string; via?: string };

/** A sticker pack, as givenTo reads it: its ID and its stickers. */
type Pack = { id: string; stickers: number[] };

/** givenTo maps each sticker number to the agents whose settings give it
 * (chat's profiles[].media.stickers, then .packs through packs), in the
 * profiles' order, once each: directly when both. Settings it cannot read
 * give nothing. */
export function givenTo(chatSettings: unknown, packs: Pack[] = []): Map<number, Agent[]> {
  const out = new Map<number, Agent[]>();
  const profiles = (chatSettings as { profiles?: unknown } | undefined)?.profiles;
  if (!Array.isArray(profiles)) {
    return out;
  }
  const members = new Map(packs.map((p) => [p.id, p.stickers]));
  const give = (id: number, agent: Agent) => {
    if (!out.get(id)?.some((a) => a.name === agent.name)) {
      out.set(id, [...(out.get(id) ?? []), agent]);
    }
  };
  for (const p of profiles) {
    const name = typeof p?.name === "string" ? p.name : "";
    if (!name) {
      continue;
    }
    const title = typeof p.label === "string" && p.label ? p.label : name;
    const stickers = p?.media?.stickers;
    for (const id of Array.isArray(stickers) ? stickers : []) {
      if (typeof id === "number") {
        give(id, { name, title });
      }
    }
    const given = p?.media?.packs;
    for (const pack of Array.isArray(given) ? given : []) {
      for (const id of (typeof pack === "string" && members.get(pack)) || []) {
        give(id, { name, title, via: pack });
      }
    }
  }
  return out;
}

/** sentQuery is the event log search for the chat.reply records that sent
 * sticker id. It matches how chat.reply's payload is written, its sticker
 * an object whose first field is id: {"sticker":{"id":3,"ref":…}}. When
 * chat.reply's sticker changes shape, change this with it. */
export function sentQuery(id: number): string {
  return `"sticker":{"id":${id},`;
}

/** mib writes a size in MiB, one decimal. */
export function mib(bytes: number): string {
  return `${(bytes / (1 << 20)).toFixed(1)} MiB`;
}

/** size writes a picture's size: KiB under one MiB, else MiB. */
export function size(bytes: number): string {
  return bytes < 1 << 20 ? `${Math.max(1, Math.round(bytes / 1024))} KiB` : mib(bytes);
}

/** usage is the card's note on the collection: how many, and their size. */
export function usage(stickers: { bytes: number }[]): string {
  return `${stickers.length} · ${mib(stickers.reduce((n, s) => n + s.bytes, 0))}`;
}

/** accepted is the picture types the media module keeps. */
export const accepted = ["image/png", "image/jpeg", "image/gif"];

/** next is the sticker to select once id is removed: the one after it,
 * else the one before, else none. */
export function next(ids: number[], id: number): number | undefined {
  const i = ids.indexOf(id);
  if (i < 0) {
    return undefined;
  }
  return ids[i + 1] ?? ids[i - 1];
}

/** allStickers and noPack are the views of the card besides one pack's. */
export const allStickers = "*all";
export const noPack = "*none";

/** inView reports whether a sticker in packs shows in view: allStickers,
 * noPack, or a pack's ID. */
export function inView(packs: string[], view: string): boolean {
  if (view === allStickers) {
    return true;
  }
  return view === noPack ? packs.length === 0 : packs.includes(view);
}

/** matches reports whether a sticker matches what the filter says: its
 * number, with or without #, or text in its note or summary, ignoring
 * case. An empty filter matches all. */
export function matches(s: { id: number; note: string; summary: string }, filter: string): boolean {
  const q = filter.trim().toLowerCase();
  if (q === "") {
    return true;
  }
  if (/^#?\d+$/.test(q)) {
    return s.id === Number(q.replace("#", ""));
  }
  return s.note.toLowerCase().includes(q) || s.summary.toLowerCase().includes(q);
}

/** packIDPattern is what a pack's ID matches, as the media module checks. */
export const packIDPattern = /^[a-z0-9][a-z0-9-]{0,31}$/;

/** packIDProblem says what is wrong with a pack ID, or nothing. */
export function packIDProblem(id: string, taken: string[]): string {
  if (id === "") {
    return "Give it an ID.";
  }
  if (!packIDPattern.test(id)) {
    return "Use lowercase letters, digits, and hyphens, starting with a letter or digit, at most 32.";
  }
  return taken.includes(id) ? "A pack with this ID exists." : "";
}

/** onlyIn counts the stickers of pack that are in no other pack. */
export function onlyIn(stickers: { packs: string[] }[], pack: string): number {
  return stickers.filter((s) => s.packs.length === 1 && s.packs[0] === pack).length;
}

/** moved is list with its item at i moved one place earlier (step -1) or
 * later (step 1); the list as it was when that would leave it. */
export function moved<T>(list: readonly T[], i: number, step: -1 | 1): T[] {
  const out = [...list];
  const j = i + step;
  if (i < 0 || i >= out.length || j < 0 || j >= out.length) {
    return out;
  }
  [out[i], out[j]] = [out[j] as T, out[i] as T];
  return out;
}

/** shownTo counts an agent's list as chat builds it from the agent's
 * settings: the stickers named, then each pack's, in the order of the
 * packs, each once, leaving out those no longer kept, packs no longer there,
 * and stickers with no text, which agents are not shown. given is how many
 * that is; seen, how many of them the agent sees: the first max. */
export function shownTo(
  stickers: unknown,
  packs: unknown,
  max: number,
  kept: { id: number; text: string }[],
  all: Pack[],
): { given: number; seen: number } {
  const shown = new Set(kept.filter((s) => s.text).map((s) => s.id));
  const members = new Map(all.map((p) => [p.id, p.stickers]));
  const list = new Set<number>();
  for (const id of Array.isArray(stickers) ? stickers : []) {
    if (typeof id === "number" && shown.has(id)) {
      list.add(id);
    }
  }
  for (const pack of Array.isArray(packs) ? packs : []) {
    for (const id of (typeof pack === "string" && members.get(pack)) || []) {
      if (shown.has(id)) {
        list.add(id);
      }
    }
  }
  return { given: list.size, seen: Math.min(list.size, max) };
}

/** seenNote is the line under an agent's sticker pickers: how many it sees,
 * and, past max, that it sees only the first; over says it is past. */
export function seenNote(agent: string, given: number, max: number): { text: string; over: boolean } {
  const n = (k: number) => `${k} ${k === 1 ? "sticker" : "stickers"}`;
  if (given > max) {
    return {
      text: `${agent} is given ${n(given)} and sees the first ${max}: single stickers first, then packs in order.`,
      over: true,
    };
  }
  return { text: given === 0 ? `${agent} sees no stickers.` : `${agent} sees ${n(given)}.`, over: false };
}
