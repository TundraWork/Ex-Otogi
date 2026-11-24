// The language model providers and their models, as the settings that name
// them see them. A field of format llm-provider names a provider; one of
// format llm-model names a model of the provider the field named provider
// beside it names, and one of llm-thinking a thinking level of that model.
// The lists are offered, never enforced: the server decides what saves.

/** Model is a model as a provider's list describes it: only the ID is sure. */
export type Model = {
  id: string;
  display_name?: string;
  input_tokens?: number;
  output_tokens?: number;
  context_tokens?: number;
  input_modalities?: string[] | null;
  thinking_levels?: string[] | null;
};

/** Provider is a configured provider with the models last fetched from it. */
export type Provider = {
  name: string;
  kind: "gemini" | "openai";
  fetched_at?: string;
  fetched_by?: string;
  models: Model[];
};

/** kinds names the kinds of provider. */
export const kinds = { gemini: "Gemini", openai: "OpenAI-compatible" } as const;

/** sibling is the path of the field named key in the object of the field at path. */
export function sibling(path: string, key: string): string {
  return path.replace(/[^.[\]]+$/, key);
}

/** providerChoices are the providers a field can name: the configured ones,
 * and the one it names now when that is not configured, without a provider. */
export function providerChoices(providers: Provider[], value: string): { name: string; provider?: Provider }[] {
  const out: { name: string; provider?: Provider }[] = providers.map((p) => ({ name: p.name, provider: p }));
  if (value && !providers.some((p) => p.name === value)) out.push({ name: value });
  return out;
}

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;

/** providerSummary says how many models were fetched from a provider, and
 * its kind: the count first, as the part a narrow select keeps. */
export function providerSummary(p: Provider): string {
  return `${p.fetched_at ? plural(p.models.length, "model") : "No list"} · ${kinds[p.kind]}`;
}

/** ModelState is a model field's value against its provider's list. */
export type ModelState =
  | { kind: "empty" }
  | { kind: "no-list" }
  | { kind: "listed"; model: Model }
  | { kind: "unlisted"; fetchedAt: string };

/** modelState finds a model field's value in its provider's list. */
export function modelState(value: string, provider: Provider | undefined): ModelState {
  if (!value) return { kind: "empty" };
  if (!provider?.fetched_at) return { kind: "no-list" };
  const model = provider.models.find((m) => m.id === value);
  return model ? { kind: "listed", model } : { kind: "unlisted", fetchedAt: provider.fetched_at };
}

/** matches says whether a model's ID or display name has the text typed. */
export function matches(m: Model, query: string): boolean {
  const q = query.trim().toLowerCase();
  return m.id.toLowerCase().includes(q) || (m.display_name ?? "").toLowerCase().includes(q);
}

const compact = new Intl.NumberFormat("en", { notation: "compact" });

/** limits reads a model's token limits shortly: 1M in · 66K out. */
export function limits(m: Model): string {
  return [
    m.input_tokens && `${compact.format(m.input_tokens)} in`,
    m.context_tokens && `${compact.format(m.context_tokens)} context`,
    m.output_tokens && `${compact.format(m.output_tokens)} out`,
  ]
    .filter(Boolean)
    .join(" · ");
}

/** takesImages says whether a model takes images; undefined when its list does not say. */
export function takesImages(m: Model): boolean | undefined {
  return m.input_modalities?.length ? m.input_modalities.includes("image") : undefined;
}

/** thinkingNote says why a thinking level may not work with a model: its
 * list gives the levels it accepts, and this is not one. */
export function thinkingNote(level: string, m: Model | undefined): string | undefined {
  if (!m?.thinking_levels?.length || m.thinking_levels.includes(level)) return undefined;
  return `${m.id} does not list it`;
}
