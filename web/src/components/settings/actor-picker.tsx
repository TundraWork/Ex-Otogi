import { Autocomplete } from "@base-ui/react/autocomplete";
import { useQuery } from "@tanstack/react-query";
import { ChevronsUpDownIcon, X } from "lucide-react";
import { type ReactNode, type Ref, useRef, useState } from "react";
import { LoadError } from "@/components/load-error";
import { selectTriggerIconClassName } from "@/components/ui/select";
import { type Person, seenIn, withID } from "@/lib/people";
import { since } from "@/lib/time";
import { cn } from "@/lib/utils";
import { peopleQuery, personQuery } from "@/queries/people";
import type { Labelling } from "./schema-form";

// The controls of the fields of format actor, which name a person by their
// platform-qualified ID: a search over the people the bot has seen, which
// also takes any ID typed, such as one of someone who has not written yet.

const frame = (invalid: unknown) =>
  cn(
    "relative flex w-full rounded-lg border border-input bg-background bg-clip-padding text-base shadow-xs/5 ring-ring/24 transition-shadow focus-within:border-ring focus-within:ring-[3px] sm:text-sm",
    Boolean(invalid) && "border-destructive/36 focus-within:border-destructive/64 focus-within:ring-destructive/16",
  );

function PersonOption({ person }: { person: Person }) {
  const where = seenIn(person);
  return (
    <span className="flex min-w-0 flex-1 flex-col">
      <span className="flex min-w-0 items-baseline gap-2">
        <span className="min-w-0 max-w-[60%] flex-none truncate font-medium">{person.name || "No name"}</span>
        <span className="min-w-0 truncate font-mono text-muted-foreground text-xs">{person.id}</span>
      </span>
      <span className="truncate text-muted-foreground text-xs">
        Seen {since(person.last_seen)}
        {where && ` in ${where}`}
      </span>
    </span>
  );
}

/** PeopleSearch is the popup and its state for a field of format actor: it
 * offers the people the search finds, those of ids left out. */
function PeopleSearch({
  query,
  open,
  exclude = [],
  labelledBy,
  onHighlight,
  popupRef,
  children,
  ...root
}: {
  query: string;
  open: boolean;
  exclude?: string[];
  labelledBy: string;
  onHighlight?: (person: Person | undefined) => void;
  popupRef?: Ref<HTMLDivElement>;
  children: ReactNode;
} & Pick<Autocomplete.Root.Props<Person>, "value" | "onValueChange" | "onOpenChange">) {
  const { data = [], error, refetch, isPending } = useQuery({ ...peopleQuery(query), enabled: open });
  const people = data.filter((p) => !exclude.includes(p.id));
  return (
    <>
      <Autocomplete.Root
        {...root}
        items={people}
        filteredItems={people}
        itemToStringValue={(p: Person) => p.id}
        onItemHighlighted={(p) => onHighlight?.(p)}
        openOnInputClick
      >
        {children}
        <Autocomplete.Portal>
          <Autocomplete.Positioner sideOffset={4} align="start" className="z-50">
            <Autocomplete.Popup
              ref={popupRef}
              className="max-h-[min(var(--available-height),22rem)] w-[max(var(--anchor-width),20rem)] max-w-[calc(100vw-1rem)] overflow-y-auto rounded-lg border bg-popover bg-clip-padding p-1 text-foreground shadow-lg/5 outline-none"
            >
              <Autocomplete.Empty className="px-2 py-1.5 text-muted-foreground text-sm empty:hidden">
                {isPending
                  ? "Searching…"
                  : query.trim()
                    ? "No one seen matches: the ID typed is kept as it is."
                    : "No one else seen yet: type an ID, such as telegram:user:123."}
              </Autocomplete.Empty>
              <Autocomplete.List aria-labelledby={labelledBy}>
                {(p: Person) => (
                  <Autocomplete.Item
                    key={p.id}
                    value={p}
                    className="flex min-h-11 cursor-default items-center gap-2 rounded-sm px-2 py-1 text-sm outline-none data-highlighted:bg-accent data-highlighted:text-accent-foreground"
                  >
                    <PersonOption person={p} />
                  </Autocomplete.Item>
                )}
              </Autocomplete.List>
            </Autocomplete.Popup>
          </Autocomplete.Positioner>
        </Autocomplete.Portal>
      </Autocomplete.Root>
      {error && <LoadError what="the people seen" error={error} retry={() => void refetch()} />}
    </>
  );
}

const inputClass =
  "h-8.5 w-full min-w-0 bg-transparent ps-[calc(--spacing(3)-1px)] font-mono text-[13px] outline-none placeholder:font-sans placeholder:text-muted-foreground placeholder:text-sm pointer-coarse:h-11 sm:h-7.5";

function Trigger() {
  return (
    <Autocomplete.Trigger
      aria-label="Show the people seen"
      className="hit-target relative flex h-8.5 flex-none items-center rounded-e-lg px-2 text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring pointer-coarse:h-11 sm:h-7.5"
    >
      <ChevronsUpDownIcon className={selectTriggerIconClassName} />
    </Autocomplete.Trigger>
  );
}

/** ActorPicker names one person: one of the people seen, found by name or
 * ID, or any ID typed. The field holds the ID; the name, when the bot has
 * seen them, stands beside it. */
export function ActorPicker({
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
  // Opened, the list shows the people last seen; typing searches.
  const [typed, setTyped] = useState(false);
  const [open, setOpen] = useState(false);
  const { data: person } = useQuery(personQuery(value));
  return (
    <PeopleSearch
      query={typed ? value : ""}
      open={open}
      labelledBy={a["aria-labelledby"]}
      value={value}
      onValueChange={(next, details) => {
        setTyped(details.reason === "input-change");
        onChange(next);
      }}
      onOpenChange={(on) => {
        setOpen(on);
        if (!on) setTyped(false);
      }}
    >
      <div className={cn(frame(a["aria-invalid"]), "items-center")}>
        <Autocomplete.Input
          {...a}
          aria-describedby={
            [a["aria-describedby"], person?.name && `${a.id}-person`].filter(Boolean).join(" ") || undefined
          }
          onBlur={onBlur}
          spellCheck={false}
          autoCapitalize="off"
          placeholder="Search by name, or type an ID"
          className={inputClass}
        />
        {person?.name && (
          <span id={`${a.id}-person`} className="max-w-[40%] flex-none truncate ps-2 font-medium text-sm">
            {person.name}
          </span>
        )}
        <Trigger />
      </div>
    </PeopleSearch>
  );
}

/** ActorNote says where the person a field names was last seen, or that the
 * bot has seen no one of that ID: it saves all the same. */
export function ActorNote({ value }: { value: unknown }) {
  // What has no colon is no ID yet, such as a name being searched for.
  const id = typeof value === "string" && value.includes(":") ? value : "";
  const { data: person, isPending } = useQuery(personQuery(id));
  if (!id || isPending) return null;
  if (!person) return <>Not seen in any chat.</>;
  const where = seenIn(person);
  return (
    <>
      Seen {since(person.last_seen)}
      {where && ` in ${where}`}.
    </>
  );
}

function ActorChip({ id, onRemove }: { id: string; onRemove: () => void }) {
  const { data: person, isPending } = useQuery(personQuery(id));
  const name = person?.name;
  return (
    <span className="inline-flex min-w-0 max-w-full items-center gap-1.5 rounded-md border bg-linear-to-b from-white to-neutral-50 py-0.5 ps-2 pe-1 text-xs shadow-[0_1px_--theme(--color-black/4%)]">
      {name && <span className="flex-none font-medium">{name}</span>}
      <span title={id} className={cn("min-w-0 truncate font-mono", name && "text-muted-foreground")}>
        {id}
      </span>
      {!isPending && !person && (
        <span className="flex-none text-muted-foreground" title="Not seen in any chat">
          · not seen
        </span>
      )}
      <button
        type="button"
        className="relative flex-none rounded text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring pointer-coarse:after:absolute pointer-coarse:after:-inset-2"
        aria-label={`Remove ${name ? `${name} (${id})` : id}`}
        onClick={onRemove}
      >
        <X className="size-3" />
      </button>
    </span>
  );
}

/** ActorTags names several people, as Tags edits words: each a chip with
 * the person's name when the bot has seen them; Enter or a comma adds what
 * is typed, and choosing one of the people seen adds them. */
export function ActorTags({
  value,
  onChange,
  labelling: a,
}: {
  value: string[];
  onChange: (value: string[]) => void;
  labelling: Labelling;
}) {
  const [draft, setDraft] = useState("");
  const [open, setOpen] = useState(false);
  const highlighted = useRef<Person | undefined>(undefined);
  const popup = useRef<HTMLDivElement>(null);
  const add = () => {
    onChange(withID(value, draft));
    setDraft("");
  };
  return (
    <PeopleSearch
      query={draft}
      open={open}
      exclude={value}
      labelledBy={a["aria-labelledby"]}
      value={draft}
      onValueChange={(next, details) => {
        if (details.reason === "item-press") {
          onChange(withID(value, next));
          setDraft("");
        } else {
          setDraft(next);
        }
      }}
      onOpenChange={setOpen}
      popupRef={popup}
      onHighlight={(p) => {
        highlighted.current = p;
      }}
    >
      <div className={cn(frame(a["aria-invalid"]), "min-h-9 flex-wrap items-center gap-1.5 px-1.5 py-1 sm:min-h-8")}>
        {value.map((id) => (
          <ActorChip key={id} id={id} onRemove={() => onChange(value.filter((v) => v !== id))} />
        ))}
        <div className="flex min-w-40 flex-1 items-center">
          <Autocomplete.Input
            {...a}
            spellCheck={false}
            autoCapitalize="off"
            placeholder={value.length ? "Add another" : "Search by name, or type an ID"}
            // Leaving the field adds the ID typed, as Tags does; going into the list does not.
            onBlur={(e) => {
              if (!(e.relatedTarget instanceof Node && popup.current?.contains(e.relatedTarget))) add();
            }}
            className="h-7 w-full min-w-0 bg-transparent px-1 font-mono text-[13px] outline-none placeholder:font-sans placeholder:text-muted-foreground placeholder:text-sm"
            onKeyDown={(e) => {
              // With one of the people highlighted, Enter chooses them.
              if ((e.key === "Enter" && !highlighted.current) || e.key === ",") {
                e.preventDefault();
                add();
              } else if (e.key === "Backspace" && !draft && value.length) {
                onChange(value.slice(0, -1));
              }
            }}
          />
          <Trigger />
        </div>
      </div>
    </PeopleSearch>
  );
}
