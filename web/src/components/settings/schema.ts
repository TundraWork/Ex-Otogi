// The JSON Schema of module settings, as the server generates it from each
// module's Config type: object properties (ordered by x-order), arrays,
// maps (additionalProperties), and $defs references. A list whose items'
// key cannot change once saved, such as accounts' names, says which field
// that is in x-key.

export type JSONSchema = {
  type?: string | string[];
  properties?: Record<string, JSONSchema>;
  required?: string[];
  items?: JSONSchema;
  additionalProperties?: boolean | JSONSchema;
  $ref?: string;
  $defs?: Record<string, JSONSchema>;
  description?: string;
  enum?: unknown[];
  /** default is the value an unset field has. */
  default?: unknown;
  format?: string;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  minItems?: number;
  minProperties?: number;
  pattern?: string;
  patternDescription?: string;
  "x-order"?: string[];
  "x-key"?: string;
  /** x-unit is what a number counts, shown after it: MB, %. */
  "x-unit"?: string;
  /** x-placeholder is the default of a field that depends on the fields
   * beside it, which fill in its {names}: see placeholder. */
  "x-placeholder"?: string;
  /** x-ordered marks a list whose order is what the module does with it. */
  "x-ordered"?: boolean;
};

export type Kind = "string" | "integer" | "number" | "boolean" | "array" | "map" | "object";

/** resolve follows a $ref into the root's $defs. */
export function resolve(root: JSONSchema, s: JSONSchema): JSONSchema {
  if (!s.$ref) return s;
  const name = s.$ref.replace("#/$defs/", "");
  return root.$defs?.[name] ?? {};
}

export function kindOf(s: JSONSchema): Kind {
  const type = Array.isArray(s.type) ? s.type.find((t) => t !== "null") : s.type;
  if (type === "object") return s.properties ? "object" : "map";
  return (type as Kind | undefined) ?? "string";
}

export function properties(s: JSONSchema): [string, JSONSchema][] {
  const props = s.properties ?? {};
  const order = s["x-order"] ?? Object.keys(props);
  return order.filter((k) => k in props).map((k) => [k, props[k] as JSONSchema]);
}

// The words of a key that are not written as the key has them. The schema
// has no titles, so openai, a map of services, and the lists of accounts
// are named here, and in titles the keys that name a tool or a service.
const titles: Record<string, string> = {
  cli: "todou CLI",
  jev: "TypeSafe Jev",
};

const spelt: Record<string, string> = {
  api: "API",
  cli: "CLI",
  id: "ID",
  url: "URL",
  kb: "KB",
  mb: "MB",
  gb: "GB",
  eventlog: "event log",
  openai: "OpenAI-compatible services",
  telegram: "Telegram accounts",
  qq: "QQ accounts",
};

/** label turns max_tool_rounds into "Max tool rounds", and base_url into "Base URL". */
export function label(key: string): string {
  if (titles[key]) return titles[key];
  const words = key.replace(/[a-z]+/g, (word) => spelt[word] ?? word).replaceAll("_", " ");
  return words.charAt(0).toUpperCase() + words.slice(1);
}

/** noun is what one item of a list or map is called, after its title:
 * "Profiles" has a profile, "OpenAI-compatible services" a service. */
export function noun(title: string): string {
  return (title.split(" ").at(-1) ?? "").replace(/s$/, "").toLowerCase();
}

/** pathLabel reads profiles[1].model as "Profiles 2 › Model". */
export function pathLabel(path: string): string {
  if (!path) return "Settings";
  return path
    .split(".")
    .map((part) => label(part).replace(/\[(\d+)\]/g, (_, i) => ` ${Number(i) + 1}`))
    .join(" › ");
}

/** placeholder is what an empty field shows: its default, filled in from
 * the fields beside it (siblings, its object's values) when it depends on
 * them, and none until they are set; an optional number without one has
 * the module's own. */
export function placeholder(s: JSONSchema, siblings: unknown, required: boolean): string {
  const template = s["x-placeholder"];
  if (template) {
    const values = (siblings ?? {}) as Record<string, unknown>;
    let missing = false;
    const filled = template.replace(/\{(\w+)\}/g, (_, key: string) => {
      const v = values[key];
      if (typeof v !== "string" || !v) missing = true;
      return String(v);
    });
    return missing ? "" : `Default ${filled}`;
  }
  if (s.default !== undefined) return `Default ${s.default}`;
  if (s.format === "duration") return "Like 5m";
  return ["integer", "number"].includes(kindOf(s)) && !required ? "Default" : "";
}

/** entrySchema is the schema of a map's values. */
export function entrySchema(s: JSONSchema): JSONSchema {
  return typeof s.additionalProperties === "object" ? s.additionalProperties : {};
}

/** check validates one value against its schema, as the server will. */
export function check(s: JSONSchema, value: unknown, required: boolean): string | undefined {
  const empty = value === undefined || value === null || value === "";
  if (empty) return required ? "Required" : undefined;
  if (s.enum && !s.enum.includes(value)) return `One of ${s.enum.join(", ")}`;
  if (typeof value === "string") {
    if (s.minLength !== undefined && value.length < s.minLength) return "Required";
    if (s.maxLength !== undefined && value.length > s.maxLength) return `At most ${s.maxLength} characters`;
    if (s.pattern && !new RegExp(s.pattern).test(value))
      return `Should be ${s.patternDescription ?? `like ${s.pattern}`}`;
  }
  if (typeof value === "number") {
    if (Number.isNaN(value)) return "A number";
    if (kindOf(s) === "integer" && !Number.isInteger(value)) return "A whole number";
    if (s.minimum !== undefined && value < s.minimum) return `At least ${s.minimum}`;
    if (s.maximum !== undefined && value > s.maximum) return `At most ${s.maximum}`;
  }
  if (Array.isArray(value) && s.minItems !== undefined && value.length < s.minItems) {
    return `At least ${s.minItems}`;
  }
  return undefined;
}

export type Problem = { path: string; message: string };

/** validate checks whole settings against their schema, whatever part of
 * the form is open, and lists the problems in the order of the form. */
export function validate(root: JSONSchema, value: unknown, schema = root, required = true, path = ""): Problem[] {
  const s = resolve(root, schema);
  const kind = kindOf(s);
  const at = (key: string) => (path ? `${path}.${key}` : key);
  if (kind === "object") {
    if (value === undefined && !required) return [];
    const v = (value ?? {}) as Record<string, unknown>;
    return properties(s).flatMap(([key, prop]) =>
      validate(root, v[key], prop, s.required?.includes(key) ?? false, at(key)),
    );
  }
  const message = check(s, value, required);
  if (message) return [{ path, message }];
  if (kind === "map" && value) {
    const entries = Object.entries(value);
    if (s.minProperties !== undefined && entries.length < s.minProperties) {
      return [{ path, message: `At least ${s.minProperties}` }];
    }
    // An entry's empty text is a value: clean keeps it.
    return entries.flatMap(([key, v]) => validate(root, v, entrySchema(s), v !== "", at(key)));
  }
  if (kind === "array" && Array.isArray(value)) {
    const item = resolve(root, s.items ?? {});
    if (["object", "map"].includes(kindOf(item))) {
      return value.flatMap((v, i) => validate(root, v, item, true, `${path}[${i}]`));
    }
    // A list of words is one field: its first bad word is its problem.
    const bad = value.map((v) => check(item, v, true)).find(Boolean);
    return bad ? [{ path, message: bad }] : [];
  }
  return [];
}

/** blank is a new item's value: an object with nothing set, a number not yet given. */
export function blank(root: JSONSchema, s: JSONSchema): unknown {
  const r = resolve(root, s);
  switch (kindOf(r)) {
    case "object":
    case "map":
      return {};
    case "array":
      return [];
    case "boolean":
      return false;
    case "integer":
    case "number":
      return undefined;
    default:
      return "";
  }
}

/** itemTitle names a list item by its name, its model, or else the first
 * value it has; an item with none has no title. A field of format actor is
 * the person's name in people, by ID, when it has theirs. */
export function itemTitle(s: JSONSchema, value: unknown, people?: ReadonlyMap<string, string>): string | undefined {
  const item = (value ?? {}) as Record<string, unknown>;
  for (const key of ["name", "label", "model", ...properties(s).map(([k]) => k)]) {
    const v = item[key];
    if (typeof v === "string" && v && s.properties?.[key]?.format === "actor") return people?.get(v) || v;
    if ((typeof v === "string" && v) || typeof v === "number") return String(v);
  }
  return undefined;
}

/** removedFrom follows paths past the removal of a list's item: those in
 * the item go, and those in the items after it move up one. */
export function removedFrom(paths: ReadonlySet<string>, list: string, index: number): Set<string> {
  const next = new Set<string>();
  for (const path of paths) {
    const at = path.startsWith(`${list}[`) ? Number.parseInt(path.slice(list.length + 1), 10) : -1;
    if (at < index) next.add(path);
    else if (at > index) next.add(`${list}[${at - 1}${path.slice(path.indexOf("]", list.length))}`);
  }
  return next;
}

/** swapped follows paths past two items of a list trading places. */
export function swapped(paths: ReadonlySet<string>, list: string, a: number, b: number): Set<string> {
  const next = new Set<string>();
  for (const path of paths) {
    const at = path.startsWith(`${list}[`) ? Number.parseInt(path.slice(list.length + 1), 10) : -1;
    const to = at === a ? b : at === b ? a : at;
    next.add(to === at ? path : `${list}[${to}${path.slice(path.indexOf("]", list.length))}`);
  }
  return next;
}

/** clean drops unset optional values, which the module defaults. A map
 * keeps its empty entries: there the key is the setting. */
export function clean(root: JSONSchema, value: unknown, schema = root): unknown {
  const s = resolve(root, schema);
  if (Array.isArray(value)) return value.map((v) => clean(root, v, s.items ?? {}));
  if (value && typeof value === "object") {
    const map = kindOf(s) === "map";
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value)) {
      if (v === undefined || v === null || (typeof v === "number" && Number.isNaN(v))) continue;
      if (v === "" && !map) continue;
      out[k] = clean(root, v, map ? entrySchema(s) : (s.properties?.[k] ?? {}));
    }
    return out;
  }
  return value;
}

/** Change is a value that differs; moved, a list whose items only changed
 * places, from one order to the other. */
export type Change = { path: string; from: unknown; to: unknown; moved?: true };

/** changes lists what differs between two settings, by path. In a list of
 * objects, the items that stay the same before and after the changed ones
 * are not changes, so removing one item reports that item only, and items
 * that only changed places are one change, of the list's order. */
export function changes(before: unknown, after: unknown, path = ""): Change[] {
  const isObj = (v: unknown) => v !== null && typeof v === "object" && !Array.isArray(v);
  const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
  if (isObj(before) && isObj(after)) {
    const a = before as Record<string, unknown>;
    const b = after as Record<string, unknown>;
    return [...new Set([...Object.keys(a), ...Object.keys(b)])].flatMap((k) =>
      changes(a[k], b[k], path ? `${path}.${k}` : k),
    );
  }
  if (Array.isArray(before) && Array.isArray(after) && [...before, ...after].some(isObj)) {
    const short = Math.min(before.length, after.length);
    let head = 0;
    while (head < short && same(before[head], after[head])) head++;
    let tail = 0;
    while (tail < short - head && same(before.at(-1 - tail), after.at(-1 - tail))) tail++;
    const sorted = (l: unknown[]) => l.map((v) => JSON.stringify(v)).sort();
    if (head < short && same(sorted(before), sorted(after))) return [{ path, from: before, to: after, moved: true }];
    return Array.from({ length: Math.max(before.length, after.length) - head - tail }, (_, n) => {
      const i = head + n;
      return changes(
        i < before.length - tail ? before[i] : undefined,
        i < after.length - tail ? after[i] : undefined,
        `${path}[${i}]`,
      );
    }).flat();
  }
  return same(before, after) ? [] : [{ path, from: before, to: after }];
}
