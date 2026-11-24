// The people the bot has seen, as the fields of format actor name them: by
// platform-qualified actor ID, such as telegram:user:123. The people seen are
// offered, never enforced: an ID no chat has seen saves as well.

import { chatName } from "./chats.ts";

/** Person is someone the bot has seen, as GET /api/people lists them. */
export type Person = {
  id: string;
  name?: string;
  last_seen: string;
  chats: { id: string; name?: string; last_seen: string }[];
};

/** seenIn names the chats a person was last seen in: "Friends, Group 7C2E04D1…". */
export function seenIn(p: Person): string {
  return p.chats.map((c) => c.name || chatName(undefined, c.id)).join(", ");
}

/** exactly is the person of an ID among the people a search found, if any. */
export function exactly(people: Person[] | undefined, id: string): Person | undefined {
  return id ? people?.find((p) => p.id === id) : undefined;
}

/** withID is a list of IDs with one more: what was typed, trimmed, unless
 * it is nothing or already there. */
export function withID(ids: string[], typed: string): string[] {
  const id = typed.trim();
  return id && !ids.includes(id) ? [...ids, id] : ids;
}
