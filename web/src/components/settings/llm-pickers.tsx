import { Autocomplete } from "@base-ui/react/autocomplete";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { format } from "date-fns";
import { Check, ChevronsUpDownIcon, ImageOff } from "lucide-react";
import { type ReactNode, useState } from "react";
import { LoadError } from "@/components/load-error";
import { StatusDot } from "@/components/status-dot";
import { selectTriggerIconClassName } from "@/components/ui/select";
import {
  limits,
  type Model,
  matches,
  modelState,
  type Provider,
  providerChoices,
  providerSummary,
  takesImages,
  thinkingNote,
} from "@/lib/llm";
import { cn } from "@/lib/utils";
import { llmProvidersQuery } from "@/queries/system";
import { EnumSelect, type EnumSelectProps } from "./enum-select";
import type { Labelling } from "./schema-form";

// The controls of the fields that name a language model provider, one of
// its models, and a thinking level (see lib/llm). Each reads the providers
// and their lists itself; what it offers never decides what saves.

function useProviders() {
  return useQuery(llmProvidersQuery);
}

/** LLMLink goes to the Language models page, inline in a note. */
function LLMLink({ children }: { children: ReactNode }) {
  return (
    <Link
      to="/settings/llm"
      className="hit-target relative rounded-sm font-medium text-foreground underline underline-offset-2 outline-none hover:no-underline focus-visible:ring-2 focus-visible:ring-ring"
    >
      {children}
    </Link>
  );
}

function ProviderChoice({ name, provider }: { name: string; provider?: Provider }) {
  return (
    <span className="flex min-w-0 items-center gap-3">
      <span className="max-w-[60%] flex-none truncate font-mono text-[13px]">{name}</span>
      {provider ? (
        <span className="min-w-0 flex-1 truncate text-right text-muted-foreground text-xs">
          {providerSummary(provider)}
        </span>
      ) : (
        <span className="flex flex-1 items-center justify-end gap-2 text-destructive-foreground text-xs">
          <StatusDot tone="error" /> Not configured
        </span>
      )}
    </span>
  );
}

/** ProviderPicker chooses one of the language model providers set under
 * Language models. A saved name no longer set there stays chosen, flagged. */
export function ProviderPicker({
  value,
  onChange,
  onBlur,
  labelling: a,
}: {
  value: string;
  onChange: (value: string) => void;
  onBlur?: () => void;
  labelling: Labelling;
}) {
  const { data: providers = [], error, refetch } = useProviders();
  const choices = providerChoices(providers, value);
  return (
    <>
      <EnumSelect
        labelling={a}
        onBlur={onBlur}
        value={value}
        onChange={(next) => onChange(next)}
        items={choices.map((c) => ({ value: c.name, label: <ProviderChoice {...c} /> }))}
        none={a["aria-required"] ? undefined : "None"}
        placeholder="Choose a provider"
      />
      {error && <LoadError what="the language model providers" error={error} retry={() => void refetch()} />}
    </>
  );
}

/** ProviderNote says what is wrong with the provider a field names, if anything. */
export function ProviderNote({ value }: { value: unknown }) {
  const { data: providers } = useProviders();
  if (!providers) return null;
  if (providers.length === 0) {
    return (
      <>
        No provider is set yet: add one in <LLMLink>Language models</LLMLink>.
      </>
    );
  }
  if (typeof value !== "string" || !value || providers.some((p) => p.name === value)) return null;
  return (
    <span className="text-destructive-foreground">
      Not configured in <LLMLink>Language models</LLMLink>: what uses it cannot be built until a provider of this name
      is.
    </span>
  );
}

const fetchedOn = (at: string) => format(new Date(at), "MMM d");

function ModelOption({ model }: { model: Model }) {
  const sized = limits(model);
  return (
    <span className="flex min-w-0 flex-1 flex-col">
      <span className="flex min-w-0 items-center gap-2">
        <span className="min-w-0 truncate font-mono text-[13px]">{model.id}</span>
        {takesImages(model) === false && (
          <span className="flex flex-none items-center gap-1 text-warning-foreground text-xs">
            <ImageOff className="size-3.5" aria-hidden /> No images
          </span>
        )}
      </span>
      {(model.display_name || sized) && (
        <span className="truncate text-muted-foreground text-xs">
          {[model.display_name, sized].filter(Boolean).join(" · ")}
        </span>
      )}
    </span>
  );
}

/** ModelPicker names a model of the provider beside it: one of the models
 * last fetched from it, found by ID or display name, or any name typed. */
export function ModelPicker({
  value,
  provider: name,
  onChange,
  onBlur,
  labelling: a,
}: {
  value: string;
  provider: unknown;
  onChange: (value: string) => void;
  onBlur?: () => void;
  labelling: Labelling;
}) {
  const { data: providers } = useProviders();
  const models = providers?.find((p) => p.name === name)?.models ?? [];
  // Opened, the list shows every model; typing narrows it.
  const [typed, setTyped] = useState(false);
  return (
    <Autocomplete.Root
      items={models}
      filteredItems={typed ? models.filter((m) => matches(m, value)) : models}
      value={value}
      onValueChange={(next, details) => {
        setTyped(details.reason === "input-change");
        onChange(next);
      }}
      onOpenChange={(open) => open || setTyped(false)}
      itemToStringValue={(m: Model) => m.id}
      openOnInputClick
    >
      <div
        className={cn(
          "relative flex w-full items-center rounded-lg border border-input bg-background bg-clip-padding text-base shadow-xs/5 ring-ring/24 transition-shadow focus-within:border-ring focus-within:ring-[3px] sm:text-sm",
          a["aria-invalid"] &&
            "border-destructive/36 focus-within:border-destructive/64 focus-within:ring-destructive/16",
        )}
      >
        <Autocomplete.Input
          {...a}
          onBlur={onBlur}
          spellCheck={false}
          autoCapitalize="off"
          placeholder={models.length ? "Choose or type a model ID" : "Type a model ID"}
          className="h-8.5 w-full min-w-0 bg-transparent ps-[calc(--spacing(3)-1px)] font-mono text-[13px] outline-none placeholder:font-sans placeholder:text-muted-foreground placeholder:text-sm pointer-coarse:h-11 sm:h-7.5"
        />
        {models.length > 0 && (
          <Autocomplete.Trigger
            aria-label="Show the models fetched"
            className="hit-target relative flex h-8.5 flex-none items-center rounded-e-lg px-2 text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring pointer-coarse:h-11 sm:h-7.5"
          >
            <ChevronsUpDownIcon className={selectTriggerIconClassName} />
          </Autocomplete.Trigger>
        )}
      </div>
      <Autocomplete.Portal>
        <Autocomplete.Positioner sideOffset={4} align="start" className="z-50">
          <Autocomplete.Popup className="max-h-[min(var(--available-height),22rem)] w-[max(var(--anchor-width),20rem)] max-w-[calc(100vw-1rem)] overflow-y-auto rounded-lg border bg-popover bg-clip-padding p-1 text-foreground shadow-lg/5 outline-none">
            <Autocomplete.Empty className="px-2 py-1.5 text-muted-foreground text-sm empty:hidden">
              No model fetched matches: the ID typed is kept as it is.
            </Autocomplete.Empty>
            <Autocomplete.List aria-labelledby={a["aria-labelledby"]}>
              {(m: Model) => (
                <Autocomplete.Item
                  key={m.id}
                  value={m}
                  className="flex min-h-9 cursor-default items-center gap-2 rounded-sm px-2 py-1 outline-none data-highlighted:bg-accent data-highlighted:text-accent-foreground pointer-coarse:min-h-11"
                >
                  <Check className={cn("size-4 flex-none", m.id !== value && "invisible")} aria-hidden />
                  <ModelOption model={m} />
                </Autocomplete.Item>
              )}
            </Autocomplete.List>
          </Autocomplete.Popup>
        </Autocomplete.Positioner>
      </Autocomplete.Portal>
    </Autocomplete.Root>
  );
}

/** ModelNote says how a model field's value stands against its provider's
 * list: what the list says of it, that it is not in the list, or where to
 * fetch one. */
export function ModelNote({ value, provider: name }: { value: unknown; provider: unknown }) {
  const { data: providers } = useProviders();
  const provider = providers?.find((p) => p.name === name);
  if (!provider) return null; // the provider's own note says why
  const state = modelState(typeof value === "string" ? value : "", provider);
  switch (state.kind) {
    case "no-list":
    case "empty":
      return provider.fetched_at ? null : (
        <>
          No model list fetched from {provider.name}: fetch one in <LLMLink>Language models</LLMLink> to choose from it.
        </>
      );
    case "unlisted":
      return (
        <span className="text-warning-foreground">
          Not in the list fetched {fetchedOn(state.fetchedAt)}. It saves anyway: the provider may still take it.
        </span>
      );
    case "listed": {
      const m = state.model;
      const about = [m.display_name, limits(m), takesImages(m) === false && "takes no images"].filter(Boolean);
      return about.length ? about.join(" · ") : null;
    }
  }
}

/** ThinkingSelect chooses a thinking level, marking in the list those the
 * model beside it does not accept when its list says which it does. The
 * chosen one shows as itself: ThinkingNote says, under the field, when the
 * model does not list it. */
export function ThinkingSelect({
  provider: name,
  model,
  ...props
}: EnumSelectProps & { provider: unknown; model: unknown }) {
  const { data: providers } = useProviders();
  const m = providers?.find((p) => p.name === name)?.models.find((x) => x.id === model);
  return (
    <EnumSelect
      {...props}
      items={props.items.map((item) => {
        const note = thinkingNote(item.value, m);
        return note
          ? {
              ...item,
              chosen: item.label,
              label: (
                <span className="flex min-w-0 items-center gap-3">
                  <span className="min-w-0 flex-1 truncate">{item.label}</span>
                  <span className="flex-none text-warning-foreground text-xs">{note}</span>
                </span>
              ),
            }
          : item;
      })}
    />
  );
}

/** ThinkingNote says when the model beside a thinking field does not list
 * the level chosen. */
export function ThinkingNote({ value, provider: name, model }: { value: unknown; provider: unknown; model: unknown }) {
  const { data: providers } = useProviders();
  const m = providers?.find((p) => p.name === name)?.models.find((x) => x.id === model);
  const note = typeof value === "string" && value ? thinkingNote(value, m) : undefined;
  return note ? <span className="text-warning-foreground">{note}.</span> : null;
}
