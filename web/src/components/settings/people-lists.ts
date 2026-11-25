// The lists in settings that name people, as a chat's page offers to add the
// people seen there to them: a list at the top of the settings whose items
// are a person (format actor, such as the general section's operators), or
// objects with a field that is (such as chat's people, by user). They are
// found in each subject's schema, so a module adding such a list needs
// nothing more.

import { blank, type JSONSchema, kindOf, properties, resolve } from "./schema.ts";

/** PeopleList is a list in a subject's settings that names people. */
export type PeopleList = {
  /** key is the list's field, at the top of the settings. */
  key: string;
  /** person is the field of an item that names the person; empty when the
   * items are the people. */
  person: string;
  root: JSONSchema;
  item: JSONSchema;
};

/** peopleLists are the lists naming people in settings of the schema root. */
export function peopleLists(root: JSONSchema): PeopleList[] {
  const out: PeopleList[] = [];
  for (const [key, s] of properties(root)) {
    const list = resolve(root, s);
    if (kindOf(list) !== "array" || !list.items) continue;
    const item = resolve(root, list.items);
    if (item.format === "actor") {
      out.push({ key, person: "", root, item });
      continue;
    }
    if (kindOf(item) !== "object") continue;
    const person = properties(item).find(([, p]) => resolve(root, p).format === "actor")?.[0];
    if (person) out.push({ key, person, root, item });
  }
  return out;
}

const itemsOf = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);

const idOf = (list: PeopleList, item: unknown): unknown =>
  list.person ? (item as Record<string, unknown> | null)?.[list.person] : item;

/** listed is where the person of id is in the list of settings: the item's
 * place, and its own name, as list items are titled by it. An item without
 * one is called by the person, whom the chat's page names already. */
export function listed(list: PeopleList, settings: unknown, id: string): { index: number; name?: string } | undefined {
  const items = itemsOf((settings as Record<string, unknown> | null)?.[list.key]);
  const index = items.findIndex((item) => idOf(list, item) === id);
  if (index < 0) return undefined;
  const item = (list.person ? items[index] : {}) as Record<string, unknown>;
  const name = ["name", "label"]
    .filter((k) => k !== list.person)
    .map((k) => item[k])
    .find((v): v is string => typeof v === "string" && v !== "");
  return { index, name };
}

/** withPerson is the list's items with the person of id added at the end, as
 * a new item naming them or as their ID, unless they are there already; at
 * is their place. */
export function withPerson(
  list: PeopleList,
  items: unknown[] | undefined,
  id: string,
): { items: unknown[]; at: number; added: boolean } {
  const now = items ?? [];
  const at = now.findIndex((item) => idOf(list, item) === id);
  if (at >= 0) return { items: now, at, added: false };
  const item = list.person ? { ...(blank(list.root, list.item) as Record<string, unknown>), [list.person]: id } : id;
  return { items: [...now, item], at: now.length, added: true };
}

/** personField is the field of an item to fill once it names the person:
 * the first other one. A list of people has none. */
export function personField(list: PeopleList): string | undefined {
  return list.person ? properties(list.item).find(([k]) => k !== list.person)?.[0] : undefined;
}
