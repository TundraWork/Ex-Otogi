import { type AnyFieldApi, getBy, type ReactFormExtendedApi, useStore } from "@tanstack/react-form";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ArrowDown, ArrowUp, Check, ChevronRight, CircleAlert, Lock, Plus, Trash2, X } from "lucide-react";
import { createContext, Fragment, type ReactNode, useContext, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsiblePanel, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { reading } from "@/lib/duration";
import { sibling, toolModules } from "@/lib/llm";
import { cn, textLink } from "@/lib/utils";
import { modulesQuery } from "@/queries/modules";
import { usePeopleNames } from "@/queries/people";
import { ActorNote, ActorPicker, ActorTags } from "./actor-picker";
import { EnumSelect } from "./enum-select";
import { ModelNote, ModelPicker, ProviderNote, ProviderPicker, ThinkingNote, ThinkingSelect } from "./llm-pickers";
import {
  blank,
  entrySchema,
  itemTitle,
  type JSONSchema,
  kindOf,
  label,
  noun,
  type Problem,
  placeholder,
  properties,
  removedFrom,
  resolve,
  swapped,
} from "./schema";
import { SecretNote, SecretPicker } from "./secret-picker";

// biome-ignore lint/suspicious/noExplicitAny: settings are any module's, shaped by the schema at runtime
export type SettingsForm = ReactFormExtendedApi<any, any, any, any, any, any, any, any, any, any, any, any>;

/** Settings is what the fields of one settings form share. */
export type Settings = {
  form: SettingsForm;
  root: JSONSchema;
  /** id starts the element ID of every control: see controlId. */
  id: string;
  /** saved is the settings the form started from. */
  saved: unknown;
  /** problems are the ones to show, each on the field or row at its path. */
  problems: Problem[];
} & ReturnType<typeof useReveal>;

export const SettingsContext = createContext<Settings | null>(null);

function useSettings(): Settings {
  return useContext(SettingsContext) as Settings;
}

/** controlId is the element ID of the control at a path: a field's input,
 * a list item's header, a list's Add button; the empty path is the summary. */
export function controlId(id: string, path: string): string {
  return `${id}-${path}`;
}

/** useReveal keeps which list items are open, and opens the ones around a
 * path to move the focus to its control. It also keeps which items were
 * added since the form last started over from a revision (see settle):
 * the others are saved, and a saved item's key cannot change. */
export function useReveal(id: string) {
  const [open, setOpenPaths] = useState<ReadonlySet<string>>(new Set());
  const [added, setAdded] = useState<ReadonlySet<string>>(new Set());
  const [target, setTarget] = useState<{ path: string } | null>(null);
  useEffect(() => {
    if (!target) return;
    // A path without a control of its own, such as a required object, is its nearest row's.
    let path = target.path;
    let control = document.getElementById(controlId(id, path));
    while (!control && path) {
      path = path.replace(/(^|\.)[^.[\]]+$|\[\d+\]$/, "");
      control = document.getElementById(controlId(id, path));
    }
    control?.focus({ preventScroll: true });
    control?.scrollIntoView({ block: "center" });
  }, [target, id]);
  return {
    open,
    setOpen: (path: string, on: boolean) =>
      setOpenPaths((paths) => {
        const next = new Set(paths);
        if (on) next.add(path);
        else next.delete(path);
        return next;
      }),
    added,
    /** add notes a list item as added, not saved. */
    add: (path: string) => setAdded((paths) => new Set([...paths, path])),
    /** settle makes every item a saved one, as the form starts over from a revision. */
    settle: () => setAdded(new Set()),
    /** forget follows a removed list item: the items after it move up one. */
    forget: (list: string, index: number) => {
      setOpenPaths((paths) => removedFrom(paths, list, index));
      setAdded((paths) => removedFrom(paths, list, index));
    },
    /** swap follows two list items trading places. */
    swap: (list: string, a: number, b: number) => {
      setOpenPaths((paths) => swapped(paths, list, a, b));
      setAdded((paths) => swapped(paths, list, a, b));
    },
    reveal: (path: string) => {
      const around = [...path.matchAll(/\[\d+\]/g)].map((m) => path.slice(0, m.index + m[0].length));
      setOpenPaths((paths) => new Set([...paths, ...around]));
      setTarget({ path });
    },
  };
}

/** labelling names and describes a control by its row's title, description and error. */
function labelling(id: string, row: { doc?: string; note?: unknown; error?: string; required?: boolean }) {
  return {
    id,
    "aria-labelledby": `${id}-label`,
    "aria-describedby":
      [row.doc && `${id}-doc`, row.note && `${id}-note`, row.error && `${id}-error`].filter(Boolean).join(" ") ||
      undefined,
    "aria-invalid": row.error ? true : undefined,
    "aria-required": row.required || undefined,
  } as const;
}

export type Labelling = ReturnType<typeof labelling>;

/** describedBy adds what follows a control, by the end of its ID, to what describes it. */
const describedBy = (a: Labelling, part: string) =>
  [a["aria-describedby"], `${a.id}-${part}`].filter(Boolean).join(" ");

/** SchemaFields renders an object's fields, in the order of the type;
 * locked names the one shown read-only: the key of a saved list item. */
export function SchemaFields({ schema, path, locked }: { schema: JSONSchema; path: string; locked?: string }) {
  const { root } = useSettings();
  const s = resolve(root, schema);
  const required = new Set(s.required ?? []);
  return (
    <div className="flex flex-col divide-y divide-border/70">
      {properties(s).map(([key, prop]) => (
        <SchemaField
          key={key}
          schema={prop}
          name={path ? `${path}.${key}` : key}
          title={label(key)}
          required={required.has(key)}
          locked={key === locked}
        />
      ))}
    </div>
  );
}

type FieldProps = { schema: JSONSchema; name: string; title: string; doc?: string; required: boolean };

function SchemaField({ schema, name, title, required, locked }: FieldProps & { locked?: boolean }) {
  const { form, root, id: prefix, problems } = useSettings();
  const s = resolve(root, schema);
  const kind = kindOf(s);
  const doc = schema.description ?? s.description;
  const id = controlId(prefix, name);
  // A list of values is one control: a problem with one of them, such as tools[1], is the list's.
  const error = problems
    .filter(
      (p) =>
        p.path === name || (kind === "array" && /^\[\d+\]$/.test(p.path.slice(name.length)) && p.path.startsWith(name)),
    )
    .map((p) => p.message)
    .join("; ");
  const beside = useBeside(form, name, s);
  if (kind === "object") {
    return (
      <form.Field name={name}>
        {(field: AnyFieldApi) => {
          const set = required || field.state.value !== undefined;
          return (
            <Row
              id={id}
              title={title}
              required={required}
              doc={doc}
              error={error}
              block
              action={
                !required && (
                  <Switch
                    {...labelling(id, { doc, error })}
                    aria-labelledby={undefined}
                    aria-label={`Use ${title.toLowerCase()}`}
                    checked={set}
                    onCheckedChange={(on) => field.handleChange(on ? {} : undefined)}
                  />
                )
              }
            >
              {set && (
                <div className="w-full border-l pl-4">
                  <SchemaFields schema={s} path={name} />
                </div>
              )}
            </Row>
          );
        }}
      </form.Field>
    );
  }
  if (kind === "array" && s.items && ["object", "map"].includes(kindOf(resolve(root, s.items)))) {
    return <ObjectList schema={s} name={name} title={title} doc={doc} required={required} />;
  }
  if (kind === "map") {
    return <ObjectMap schema={s} name={name} title={title} doc={doc} required={required} />;
  }
  // A switch is always one way or the other: nothing about it is required.
  const row = { doc, error, required: required && kind !== "boolean" };
  const noted =
    locked ||
    s.items?.format === "tool-module" ||
    ["secret", "llm-provider", "llm-model", "llm-thinking", "actor"].includes(s.format ?? "");
  return (
    <form.Field name={name}>
      {(field: AnyFieldApi) => (
        <Row
          id={id}
          title={title}
          {...row}
          // A secret's row says who set the one chosen, and when; a
          // provider's or a model's, how it stands against what is set.
          note={
            locked ? (
              <span className="inline-flex items-center gap-1">
                <Lock className="size-3 flex-none" aria-hidden />
                Saved names cannot be changed.
              </span>
            ) : s.format === "secret" ? (
              <SecretNote value={field.state.value} />
            ) : s.format === "llm-provider" ? (
              <ProviderNote value={field.state.value} />
            ) : s.format === "actor" ? (
              <ActorNote value={field.state.value} />
            ) : s.items?.format === "tool-module" ? (
              <ToolModulesNote value={field.state.value} />
            ) : s.format === "llm-model" ? (
              <ModelNote value={field.state.value} provider={beside.provider} />
            ) : (
              s.format === "llm-thinking" && (
                <ThinkingNote value={field.state.value} provider={beside.provider} model={beside.model} />
              )
            )
          }
          // A group of chips has no one control the title could label.
          labels={!s.items?.enum && s.items?.format !== "tool-module"}
          block={kind === "array" || s.format === "markdown" || s.format === "text"}
          inline={kind === "boolean"}
        >
          <Control
            schema={s}
            value={field.state.value}
            onChange={field.handleChange}
            onBlur={field.handleBlur}
            labelling={labelling(id, { ...row, note: noted })}
            beside={beside}
            locked={locked}
          />
        </Row>
      )}
    </form.Field>
  );
}

/** Beside is what a field reads from the fields beside it: an llm-model
 * field the provider, an llm-thinking field the provider and the model (see
 * lib/llm), and one with an x-placeholder all of them, its siblings. */
type Beside = { provider?: unknown; model?: unknown; siblings?: unknown };

function useBeside(form: SettingsForm, name: string, schema: JSONSchema): Beside {
  const format = schema.format;
  const llm = format?.startsWith("llm-") && format !== "llm-provider";
  const provider = useStore(form.store, (s) => (llm ? getBy(s.values, sibling(name, "provider")) : undefined));
  const model = useStore(form.store, (s) =>
    format === "llm-thinking" ? getBy(s.values, sibling(name, "model")) : undefined,
  );
  const parent = name.replace(/\.?[^.[\]]+$/, "");
  const siblings = useStore(form.store, (s) =>
    schema["x-placeholder"] ? (parent ? getBy(s.values, parent) : s.values) : undefined,
  );
  return { provider, model, siblings };
}

function Row({
  id,
  title,
  required,
  doc,
  note,
  error,
  block,
  inline,
  labels,
  action,
  children,
}: {
  id: string;
  title: string;
  required?: boolean;
  doc?: string;
  /** note says more under the description, about the value as it is now. */
  note?: ReactNode;
  error?: string;
  block?: boolean;
  inline?: boolean;
  /** labels says the title is the label of the control: clicking it goes there. */
  labels?: boolean;
  action?: ReactNode;
  children: ReactNode;
}) {
  const text = (
    <>
      {title}
      {required && (
        <span className="text-destructive-foreground" title="Required" aria-hidden>
          {" *"}
        </span>
      )}
    </>
  );
  return (
    // The description runs under the title, across the row; the control sits
    // beside the title where the row is wide enough, and under the
    // description where it is not.
    <div
      className={cn(
        "grid grid-cols-1 gap-x-8 gap-y-1.5 py-3.5",
        !block && !inline && "sm:grid-cols-[minmax(0,1fr)_minmax(0,1.1fr)]",
        inline && "grid-cols-[minmax(0,1fr)_auto]",
      )}
    >
      <div
        className={cn(
          "flex min-w-0 items-center justify-between gap-3 self-start font-medium text-sm",
          inline ? "min-h-8" : !block && "sm:min-h-8",
        )}
      >
        {labels ? (
          <label id={`${id}-label`} htmlFor={id}>
            {text}
          </label>
        ) : (
          <div id={`${id}-label`}>{text}</div>
        )}
        {action}
      </div>
      {doc && (
        <p id={`${id}-doc`} className="col-span-full break-words text-muted-foreground text-xs leading-relaxed">
          {doc}
        </p>
      )}
      <div
        className={cn("min-w-0", !block && "sm:col-start-2 sm:row-start-1", inline && "col-start-2 row-start-1 pt-1.5")}
      >
        {children}
        {note && (
          <p id={`${id}-note`} className="mt-1.5 text-muted-foreground text-xs leading-relaxed empty:hidden">
            {note}
          </p>
        )}
        {error && (
          <p id={`${id}-error`} className="mt-1.5 wrap-anywhere text-destructive-foreground text-xs">
            {error}
          </p>
        )}
      </div>
    </div>
  );
}

function Control({
  schema,
  value,
  onChange,
  onBlur,
  labelling: a,
  beside,
  locked,
}: {
  schema: JSONSchema;
  value: unknown;
  onChange: (value: unknown) => void;
  onBlur?: () => void;
  labelling: Labelling;
  beside?: Beside;
  /** locked shows a text read-only: see SchemaFields. */
  locked?: boolean;
}) {
  const kind = kindOf(schema);
  if (kind === "boolean") {
    // An unset switch shows the value the module gives it.
    return <Switch {...a} checked={Boolean(value === undefined ? schema.default : value)} onCheckedChange={onChange} />;
  }
  if (kind === "array") {
    const item = schema.items ?? {};
    if (item.enum) {
      // An unset list shows the choices the module makes for it.
      const chosen = (value ?? schema.default ?? []) as unknown[];
      return (
        <Choices
          labelling={a}
          options={item.enum}
          value={chosen}
          onChange={onChange}
          unknown="not one of the choices"
        />
      );
    }
    if (item.format === "tool-module") {
      return <ToolModuleChoices labelling={a} value={(value as string[] | undefined) ?? []} onChange={onChange} />;
    }
    // An unset list shows what it is by default. When that is something, a
    // list emptied is a list of nothing; otherwise an optional one is unset.
    const unset = (schema.default ?? []) as unknown[];
    if (item.format === "actor") {
      return (
        <ActorTags
          labelling={a}
          value={((value as string[] | undefined) ?? unset) as string[]}
          onChange={(v) => onChange(v.length || unset.length || a["aria-required"] ? v : undefined)}
        />
      );
    }
    return (
      <Tags
        labelling={a}
        numeric={["integer", "number"].includes(kindOf(item))}
        value={(value as unknown[] | undefined) ?? unset}
        onChange={(v) => onChange(v.length || unset.length || a["aria-required"] ? v : undefined)}
      />
    );
  }
  if (schema.enum) {
    // A choice keeps its type: an integer's choices are saved as numbers.
    const choices = schema.enum;
    const props = {
      labelling: a,
      onBlur,
      value: value === undefined ? "" : String(value),
      onChange: (v: string) => onChange(choices.find((c) => String(c) === v)),
      items: choices.map((v) => ({ value: String(v), label: String(v) })),
      none: schema.default === undefined ? "Default" : `Default (${schema.default})`,
    };
    if (schema.format === "llm-thinking") {
      return <ThinkingSelect {...props} provider={beside?.provider} model={beside?.model} />;
    }
    return <EnumSelect {...props} />;
  }
  const shown = placeholder(schema, beside?.siblings, Boolean(a["aria-required"]));
  if (kind === "integer" || kind === "number") {
    const unit = schema["x-unit"];
    const input = (
      <Input
        {...a}
        aria-describedby={unit ? describedBy(a, "unit") : a["aria-describedby"]}
        type="number"
        inputMode={kind === "integer" ? "numeric" : "decimal"}
        // An ID is not a quantity: nothing to step through.
        className={cn(
          "tabular",
          schema.format === "id" &&
            "[&_input]:[appearance:textfield] [&_input::-webkit-inner-spin-button]:appearance-none [&_input::-webkit-outer-spin-button]:appearance-none",
        )}
        value={(value as number | undefined) ?? ""}
        min={schema.minimum}
        max={schema.maximum}
        step={kind === "integer" ? 1 : "any"}
        placeholder={shown}
        onChange={(e) => onChange(e.target.value === "" ? undefined : Number(e.target.value))}
        onBlur={onBlur}
      />
    );
    if (!unit) return input;
    return (
      <div className="flex items-center gap-2">
        {input}
        <span id={`${a.id}-unit`} className="flex-none text-muted-foreground text-sm">
          {unit}
        </span>
      </div>
    );
  }
  const text = (value as string | undefined) ?? "";
  if (schema.format === "markdown") {
    return (
      <div>
        <Textarea
          {...a}
          className="min-h-72 font-[450] text-[13.5px] leading-relaxed"
          value={text}
          onChange={(e) => onChange(e.target.value)}
          onBlur={onBlur}
          placeholder="Who the agent is, how it talks, what it knows. Markdown."
        />
        <p className="mt-1 text-right text-muted-foreground text-xs tabular">{text.length} characters</p>
      </div>
    );
  }
  if (schema.format === "text") {
    // Grows with what it holds.
    return (
      <Textarea {...a} className="min-h-0" value={text} onChange={(e) => onChange(e.target.value)} onBlur={onBlur} />
    );
  }
  if (schema.format === "secret") {
    return <SecretPicker value={text} onChange={onChange} onBlur={onBlur} labelling={a} />;
  }
  if (schema.format === "llm-provider") {
    return <ProviderPicker value={text} onChange={onChange} onBlur={onBlur} labelling={a} />;
  }
  if (schema.format === "llm-model") {
    return <ModelPicker value={text} provider={beside?.provider} onChange={onChange} onBlur={onBlur} labelling={a} />;
  }
  if (schema.format === "actor" && !locked) {
    return <ActorPicker value={text} onChange={onChange} onBlur={onBlur} labelling={a} />;
  }
  const mono = schema.format === "duration";
  // A duration says how long it is, the default's when it is unset.
  const long = mono ? reading(text || schema.default) : undefined;
  const input = (
    <Input
      {...a}
      aria-describedby={long ? describedBy(a, "reading") : a["aria-describedby"]}
      readOnly={locked}
      title={locked ? "Saved names cannot be changed" : undefined}
      className={cn(mono && "font-mono text-[13px]", locked && "bg-muted shadow-none")}
      value={text}
      placeholder={shown}
      spellCheck={!mono}
      onChange={(e) => onChange(e.target.value)}
      onBlur={onBlur}
    />
  );
  if (!long) return input;
  return (
    <div>
      {input}
      <p id={`${a.id}-reading`} className="mt-1 text-muted-foreground text-xs">
        {long}
      </p>
    </div>
  );
}

/** Choices picks any of a few values, each a chip that toggles; marks says
 * more of some, such as that a module is off. A value chosen that is not
 * one of them, such as one saved before it was renamed, stays as a chip
 * that removes it, flagged with why: unknown. */
function Choices({
  labelling: a,
  options,
  value,
  onChange,
  unknown,
  marks = {},
}: {
  labelling: Labelling;
  options: unknown[];
  value: unknown[];
  onChange: (v: unknown[]) => void;
  unknown: string;
  marks?: Record<string, string>;
}) {
  const extras = value.filter((v) => !options.includes(v));
  const chip =
    "hit-target relative inline-flex h-7 items-center gap-1.5 rounded-md border px-2.5 font-mono text-xs outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring pointer-coarse:h-9";
  return (
    // biome-ignore lint/a11y/useSemanticElements: a fieldset would need the row's title inside it as its legend
    <div
      role="group"
      id={a.id}
      tabIndex={-1}
      aria-labelledby={a["aria-labelledby"]}
      aria-describedby={a["aria-describedby"]}
      className="flex flex-wrap gap-1.5 outline-none"
    >
      {options.map((o) => {
        const on = value.includes(o);
        const mark = marks[String(o)];
        return (
          <button
            key={String(o)}
            type="button"
            aria-pressed={on}
            onClick={() =>
              onChange(
                on ? value.filter((w) => w !== o) : [...options.filter((x) => x === o || value.includes(x)), ...extras],
              )
            }
            className={cn(
              chip,
              on
                ? "border-foreground/80 bg-foreground text-background"
                : "bg-background text-muted-foreground shadow-xs/5 hover:text-foreground",
            )}
          >
            {on && <Check className="size-3" />}
            {String(o)}
            {mark && (
              <span className={cn("font-sans", on ? "text-background/72" : "text-muted-foreground")}>{mark}</span>
            )}
          </button>
        );
      })}
      {extras.map((o) => (
        <button
          key={String(o)}
          type="button"
          aria-label={`Remove ${o}: ${unknown}`}
          onClick={() => onChange(value.filter((w) => w !== o))}
          // The reason may wrap in a narrow row.
          className={cn(
            chip,
            "h-auto min-h-7 py-1 text-left pointer-coarse:h-auto pointer-coarse:min-h-9 border-destructive/36 bg-destructive/4 text-destructive-foreground hover:bg-destructive/8",
          )}
        >
          <CircleAlert className="size-3" />
          {String(o)}
          <span className="font-sans">{unknown}</span>
          <X className="size-3" />
        </button>
      ))}
    </div>
  );
}

/** ToolModuleChoices picks the modules whose tools agents may use, of those
 * that offer tools, marking the ones that are off. */
function ToolModuleChoices({
  labelling: a,
  value,
  onChange,
}: {
  labelling: Labelling;
  value: string[];
  onChange: (v: unknown) => void;
}) {
  const { data: modules } = useQuery(modulesQuery);
  // Until the modules are known, every chosen one would look unknown.
  if (!modules) return null;
  const tools = toolModules(modules);
  return (
    <Choices
      labelling={a}
      options={tools.map((t) => t.id)}
      value={value}
      onChange={(v) => onChange(v.length ? v : undefined)}
      unknown="no module offers tools by this name"
      marks={Object.fromEntries(tools.filter((t) => !t.running).map((t) => [t.id, "off"]))}
    />
  );
}

/** ToolModulesNote says which modules chosen are off: the bot refuses the
 * settings until they run, since agents can use only a running module's tools. */
function ToolModulesNote({ value }: { value: unknown }) {
  const { data: modules } = useQuery(modulesQuery);
  const chosen = (value as string[] | undefined) ?? [];
  const off = toolModules(modules ?? []).filter((t) => !t.running && chosen.includes(t.id));
  if (off.length === 0) return null;
  return (
    <span className="text-warning-foreground">
      {off.map((t, i) => (
        <Fragment key={t.id}>
          {i > 0 && (i === off.length - 1 ? " and " : ", ")}
          <Link to="/modules/$module" params={{ module: t.id }} className={cn(textLink, "font-medium")}>
            {t.id}
          </Link>
        </Fragment>
      ))}{" "}
      {off.length === 1 ? "is" : "are"} off. Turn {off.length === 1 ? "it" : "them"} on first: agents can use only a
      running module's tools, and until then the bot refuses these settings.
    </span>
  );
}

/** Tags edits a list of words or numbers: Enter or comma adds one. */
function Tags({
  labelling: a,
  numeric,
  value,
  onChange,
}: {
  labelling: Labelling;
  numeric: boolean;
  value: unknown[];
  onChange: (v: unknown[]) => void;
}) {
  const [draft, setDraft] = useState("");
  const add = () => {
    const text = draft.trim();
    const word = numeric && text ? Number(text) : text;
    // What is not a number stays in the box to be corrected.
    if (Number.isNaN(word)) return;
    if (text && !value.includes(word)) onChange([...value, word]);
    setDraft("");
  };
  return (
    <div
      className={cn(
        "flex min-h-9 w-full flex-wrap items-center gap-1.5 rounded-lg border border-input bg-background px-1.5 py-1 shadow-xs/5 focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/24 sm:min-h-8",
        a["aria-invalid"] &&
          "border-destructive/36 focus-within:border-destructive/64 focus-within:ring-destructive/16",
      )}
    >
      {value.map((word) => (
        <span
          key={String(word)}
          className="inline-flex items-center gap-1 rounded-md border bg-linear-to-b from-white to-neutral-50 py-0.5 ps-2 pe-1 font-mono text-xs shadow-[0_1px_--theme(--color-black/4%)]"
        >
          {String(word)}
          <button
            type="button"
            className="relative rounded text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring pointer-coarse:after:absolute pointer-coarse:after:-inset-2"
            aria-label={`Remove ${word}`}
            onClick={() => onChange(value.filter((w) => w !== word))}
          >
            <X className="size-3" />
          </button>
        </span>
      ))}
      <input
        {...a}
        className="min-w-24 flex-1 bg-transparent px-1 text-sm outline-none placeholder:text-muted-foreground/72"
        inputMode={numeric ? "decimal" : undefined}
        value={draft}
        placeholder={value.length ? "Add another" : "Type, then Enter"}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === ",") {
            e.preventDefault();
            add();
          } else if (e.key === "Backspace" && !draft && value.length) {
            onChange(value.slice(0, -1));
          }
        }}
        onBlur={add}
      />
    </div>
  );
}

/** RowProblems says how many problems the fields inside a row have. */
function RowProblems({ count }: { count: number }) {
  return (
    <span className="flex flex-none items-center gap-1 font-normal text-destructive-foreground text-xs">
      <CircleAlert className="size-3.5" />
      {count} {count === 1 ? "problem" : "problems"}
    </span>
  );
}

/** ObjectList edits a list of objects, such as chat agent profiles. */
function ObjectList({ schema, name, title, doc, required }: FieldProps) {
  const { form, root, id: prefix, saved, problems, open, setOpen, forget, swap, reveal, added, add } = useSettings();
  const item = resolve(root, schema.items ?? {});
  const id = controlId(prefix, name);
  const one = noun(title);
  const error = problems.find((p) => p.path === name)?.message;
  const savedCount = ((getBy(saved, name) as unknown[] | undefined) ?? []).length;
  // An item of a person, without a name of its own, is called by theirs.
  const actor = properties(item).find(([, p]) => p.format === "actor")?.[0];
  const people = usePeopleNames(
    useStore(form.store, (s) =>
      actor ? ((getBy(s.values, name) as Record<string, unknown>[] | undefined) ?? []).map((v) => v?.[actor]) : [],
    ),
  );
  return (
    <form.Field name={name} mode="array">
      {(field: AnyFieldApi) => {
        const items = (field.state.value as unknown[] | undefined) ?? [];
        return (
          <Row id={id} title={title} required={required} doc={doc} error={error} block>
            <div className="flex flex-col gap-2">
              {items.map((value, i) => {
                const path = `${name}[${i}]`;
                const itemId = controlId(prefix, path);
                // A saved item with nothing to call it by is its place in the list.
                const heading =
                  itemTitle(item, value, people) ?? (i < savedCount ? `${label(one)} ${i + 1}` : `New ${one}`);
                const own = problems.find((p) => p.path === path)?.message;
                const inside = problems.filter((p) => p.path.startsWith(`${path}.`) || p.path.startsWith(`${path}[`));
                const isOpen = open.has(path);
                /** move trades places with the item at to, keeping the focus on the
                 * button pressed, or on the other one where the item cannot go further. */
                const move = (to: number) => {
                  field.swapValues(i, to);
                  swap(name, i, to);
                  const [dir, other] = to < i ? ["up", "down"] : ["down", "up"];
                  requestAnimationFrame(() => {
                    const at = controlId(prefix, `${name}[${to}]`);
                    const button = document.getElementById(`${at}-${dir}`) as HTMLButtonElement | null;
                    (button?.disabled ? document.getElementById(`${at}-${other}`) : button)?.focus();
                  });
                };
                return (
                  <Collapsible
                    // biome-ignore lint/suspicious/noArrayIndexKey: items have no identity until named
                    key={i}
                    open={isOpen}
                    onOpenChange={(on) => setOpen(path, on)}
                    className={cn("rounded-xl border", (own || inside.length > 0) && "border-destructive/36")}
                  >
                    <div className="flex items-center gap-2 px-2 py-1.5">
                      <CollapsibleTrigger
                        id={itemId}
                        aria-describedby={own ? `${itemId}-error` : undefined}
                        className="hit-target group relative flex min-w-0 flex-1 items-center gap-1.5 rounded-md px-1.5 py-1 text-left font-medium text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
                      >
                        <ChevronRight className="size-4 flex-none text-muted-foreground transition-transform group-data-panel-open:rotate-90" />
                        <span className="min-w-0 flex-1 truncate">{heading}</span>
                        {!isOpen && inside.length > 0 && <RowProblems count={inside.length} />}
                      </CollapsibleTrigger>
                      {/* Where the order is what the module does, such as which model is tried first. */}
                      {schema["x-ordered"] && (
                        <>
                          <Button
                            id={`${itemId}-up`}
                            variant="ghost"
                            size="icon-sm"
                            aria-label={`Move ${heading} up`}
                            disabled={i === 0}
                            onClick={() => move(i - 1)}
                          >
                            <ArrowUp />
                          </Button>
                          <Button
                            id={`${itemId}-down`}
                            variant="ghost"
                            size="icon-sm"
                            aria-label={`Move ${heading} down`}
                            disabled={i === items.length - 1}
                            onClick={() => move(i + 1)}
                          >
                            <ArrowDown />
                          </Button>
                        </>
                      )}
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`Remove ${heading}`}
                        onClick={() => {
                          field.removeValue(i);
                          forget(name, i);
                        }}
                      >
                        <Trash2 />
                      </Button>
                    </div>
                    {own && (
                      <p id={`${itemId}-error`} className="px-3.5 pb-2.5 text-destructive-foreground text-xs">
                        {own}
                      </p>
                    )}
                    <CollapsiblePanel>
                      <div className="border-t px-3.5">
                        <SchemaFields
                          schema={item}
                          path={path}
                          locked={added.has(path) ? undefined : schema["x-key"]}
                        />
                      </div>
                    </CollapsiblePanel>
                  </Collapsible>
                );
              })}
              <Button
                id={id}
                aria-describedby={error ? `${id}-error` : undefined}
                variant="outline"
                size="sm"
                className="self-start"
                onClick={() => {
                  field.pushValue(blank(root, item));
                  add(`${name}[${items.length}]`);
                  reveal(`${name}[${items.length}]`);
                }}
              >
                <Plus /> Add {one}
              </Button>
            </div>
          </Row>
        );
      }}
    </form.Field>
  );
}

/** ObjectMap edits named values: objects, such as todou's agents, each
 * with its fields, or plain values, each a row of name and value. */
function ObjectMap({ schema, name, title, doc, required }: FieldProps) {
  const { form, root, id: prefix, problems } = useSettings();
  const [key, setKey] = useState("");
  const item = resolve(root, entrySchema(schema));
  const id = controlId(prefix, name);
  const one = noun(title);
  const error = problems.find((p) => p.path === name)?.message;
  return (
    <form.Field name={name}>
      {(field: AnyFieldApi) => {
        const map = (field.state.value as Record<string, unknown> | undefined) ?? {};
        const remove = (k: string) => {
          const { [k]: _, ...rest } = map;
          field.handleChange(rest);
        };
        const add = () => {
          field.handleChange({ ...map, [key]: blank(root, item) });
          setKey("");
        };
        return (
          <Row id={id} title={title} required={required} doc={doc} error={error} block>
            <div className="flex flex-col gap-2">
              {Object.keys(map).map((k) => {
                const path = `${name}.${k}`;
                const entryId = controlId(prefix, path);
                const own = problems.find((p) => p.path === path)?.message;
                const removeButton = (
                  <Button variant="ghost" size="icon-sm" aria-label={`Remove ${k}`} onClick={() => remove(k)}>
                    <Trash2 />
                  </Button>
                );
                if (kindOf(item) !== "object") {
                  return (
                    <div key={k}>
                      <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_auto] items-center gap-2">
                        <span id={`${entryId}-label`} className="truncate font-medium font-mono text-[13px]">
                          {k}
                        </span>
                        <Control
                          schema={item}
                          value={map[k]}
                          onChange={(v) => field.handleChange({ ...map, [k]: v })}
                          onBlur={field.handleBlur}
                          labelling={labelling(entryId, { error: own, required: kindOf(item) !== "string" })}
                        />
                        {removeButton}
                      </div>
                      {own && (
                        <p id={`${entryId}-error`} className="mt-1.5 text-destructive-foreground text-xs">
                          {own}
                        </p>
                      )}
                    </div>
                  );
                }
                return (
                  <div key={k} className={cn("rounded-xl border", own && "border-destructive/36")}>
                    <div className="flex items-center gap-2 px-3.5 pt-2.5">
                      {/* Focused when a problem with the entry, such as its name, is revealed. */}
                      <span
                        id={entryId}
                        tabIndex={-1}
                        aria-describedby={own ? `${entryId}-error` : undefined}
                        className="rounded-sm font-medium font-mono text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
                      >
                        {k}
                      </span>
                      <span className="ml-auto">{removeButton}</span>
                    </div>
                    {own && (
                      <p id={`${entryId}-error`} className="px-3.5 pt-1 text-destructive-foreground text-xs">
                        {own}
                      </p>
                    )}
                    <div className="mt-1.5 border-t px-3.5">
                      <SchemaFields schema={item} path={path} />
                    </div>
                  </div>
                );
              })}
              <div className="flex gap-2">
                <Input
                  id={id}
                  aria-label={`Name of a new ${one}`}
                  aria-describedby={error ? `${id}-error` : undefined}
                  className="max-w-48 font-mono text-[13px]"
                  placeholder={`${one} name`}
                  value={key}
                  onChange={(e) => setKey(e.target.value.trim())}
                  onKeyDown={(e) => {
                    if (e.key !== "Enter") return;
                    // Enter adds the entry; it does not save the form.
                    e.preventDefault();
                    if (key && !(key in map)) add();
                  }}
                />
                <Button variant="outline" size="sm" disabled={!key || key in map} onClick={add}>
                  <Plus /> Add {one}
                </Button>
              </div>
            </div>
          </Row>
        );
      }}
    </form.Field>
  );
}
