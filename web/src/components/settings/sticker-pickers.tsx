import { Toggle } from "@base-ui/react/toggle";
import { ToggleGroup } from "@base-ui/react/toggle-group";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Check, ChevronLeft, ChevronRight, CircleAlert, Plus, X } from "lucide-react";
import { type ReactNode, useState, useSyncExternalStore } from "react";
import { Button } from "@/components/ui/button";
import { Menu, MenuItem, MenuPopup, MenuTrigger } from "@/components/ui/menu";
import { Popover, PopoverClose, PopoverPopup, PopoverTitle, PopoverTrigger } from "@/components/ui/popover";
import { Sheet, SheetHeader, SheetPanel, SheetPopup, SheetTitle } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { allStickers, inView, moved, noPack, seenNote, shownTo } from "@/lib/stickers";
import { cn, textLink } from "@/lib/utils";
import { modulesQuery } from "@/queries/modules";
import { pictureURL, type Sticker, type StickerPack, stickersQuery } from "@/queries/stickers";
import type { Labelling } from "./schema-form";

// The controls of chat's media.stickers (format sticker) and media.packs
// (format sticker-pack): what is chosen, in order, as chips that can be
// moved and removed, and a way to add more, from the media module's
// stickers. While those cannot be read, the field is the plain list input.

const chip =
  "inline-flex min-h-7 items-center gap-1.5 rounded-md border bg-linear-to-b from-white to-neutral-50 py-0.5 ps-1 pe-0.5 text-xs shadow-[0_1px_--theme(--color-black/4%)] pointer-coarse:min-h-9";
const gone =
  "inline-flex min-h-7 items-center gap-1.5 rounded-md border border-destructive/36 bg-destructive/4 py-0.5 ps-2 pe-0.5 text-destructive-foreground text-xs pointer-coarse:min-h-9";
const iconButton =
  "relative inline-flex size-5 flex-none items-center justify-center rounded text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-32 pointer-coarse:after:absolute pointer-coarse:after:-inset-1.5 [&_svg]:size-3";

/** useStickers reads the stickers kept, asking only while the media module
 * runs: asked when it does not, they would fail and the panel would report
 * it, though the fields work as plain lists. unreadable says they cannot be
 * had: the module is off, or one of the two requests failed. */
function useStickers() {
  const modules = useQuery(modulesQuery);
  const running = modules.data?.some((m) => m.id === "media" && m.running) ?? false;
  const stickers = useQuery({ ...stickersQuery, enabled: running });
  return {
    data: stickers.data,
    unreadable: modules.isError || (modules.data !== undefined && !running) || stickers.isError,
  };
}

/** StickerField is the control of a media.stickers or media.packs field:
 * the pickers, or, while the stickers cannot be read (the media module not
 * running), fallback, which keeps the values as they are. */
export function StickerField({
  packs,
  labelling,
  value,
  onChange,
  fallback,
}: {
  packs: boolean;
  labelling: Labelling;
  value: unknown;
  onChange: (value: unknown) => void;
  fallback: ReactNode;
}) {
  const { data, unreadable } = useStickers();
  if (unreadable) {
    return fallback;
  }
  if (!data) {
    return <Skeleton className="h-8 w-full max-w-72 rounded-lg" />;
  }
  const list = Array.isArray(value) ? value : [];
  // Nothing chosen is the field unset, as the plain input leaves it.
  const set = (v: unknown[]) => onChange(v.length ? v : undefined);
  return packs ? (
    <PackPicker
      labelling={labelling}
      value={list.filter((v): v is string => typeof v === "string")}
      onChange={set}
      packs={data.packs}
    />
  ) : (
    <StickerPicker
      labelling={labelling}
      value={list.filter((v): v is number => typeof v === "number")}
      onChange={set}
      stickers={data.stickers}
      packs={data.packs}
    />
  );
}

/** Chosen is the chips' group: labelled by the field's title, as a
 * fieldset would be by its legend. */
function Chosen({ labelling: a, children }: { labelling: Labelling; children: ReactNode }) {
  return (
    // biome-ignore lint/a11y/useSemanticElements: a fieldset would need the row's title inside it as its legend
    <div
      role="group"
      id={a.id}
      tabIndex={-1}
      aria-labelledby={a["aria-labelledby"]}
      aria-describedby={a["aria-describedby"]}
      className="flex flex-wrap items-center gap-1.5 outline-none"
    >
      {children}
    </div>
  );
}

/** Moves are a chip's buttons: earlier, later, and remove. With one chip
 * there is nowhere to move it. */
function Moves({
  name,
  at,
  of,
  onMove,
  onRemove,
}: {
  name: string;
  at: number;
  of: number;
  onMove: (step: -1 | 1) => void;
  onRemove: () => void;
}) {
  return (
    <span className="flex items-center">
      {of > 1 && (
        <>
          <button
            type="button"
            className={iconButton}
            disabled={at === 0}
            aria-label={`Move ${name} earlier`}
            onClick={() => onMove(-1)}
          >
            <ChevronLeft />
          </button>
          <button
            type="button"
            className={iconButton}
            disabled={at === of - 1}
            aria-label={`Move ${name} later`}
            onClick={() => onMove(1)}
          >
            <ChevronRight />
          </button>
        </>
      )}
      <button type="button" className={iconButton} aria-label={`Remove ${name}`} onClick={onRemove}>
        <X />
      </button>
    </span>
  );
}

/** Gone is a chosen value the media module has not: it can only be
 * removed. */
function Gone({ name, onRemove }: { name: string; onRemove: () => void }) {
  return (
    <span className={gone}>
      <CircleAlert className="size-3 flex-none" />
      <span className="font-mono">{name}</span>
      <span>not kept any more</span>
      <button
        type="button"
        className={cn(iconButton, "text-destructive-foreground")}
        aria-label={`Remove ${name}: not kept any more`}
        onClick={onRemove}
      >
        <X />
      </button>
    </span>
  );
}

/** ToMedia points to the media page, where packs are made and stickers
 * kept: children say what to do there. */
function ToMedia({ children }: { children: ReactNode }) {
  return (
    <span className="text-muted-foreground text-xs">
      {children} on the{" "}
      <Link to="/modules/$module" params={{ module: "media" }} className={cn(textLink, "font-medium")}>
        media page
      </Link>
      .
    </span>
  );
}

/** PackPicker chooses packs, in order; Add offers the others. */
function PackPicker({
  labelling,
  value,
  onChange,
  packs,
}: {
  labelling: Labelling;
  value: string[];
  onChange: (value: string[]) => void;
  packs: StickerPack[];
}) {
  const byID = new Map(packs.map((p) => [p.id, p]));
  const others = packs.filter((p) => !value.includes(p.id));
  return (
    <Chosen labelling={labelling}>
      {value.map((id, i) => {
        const p = byID.get(id);
        const remove = () => onChange(value.filter((v) => v !== id));
        if (!p) {
          return <Gone key={id} name={id} onRemove={remove} />;
        }
        return (
          <span key={id} className={cn(chip, "ps-2")}>
            <span className="font-medium">{p.title}</span>
            <span className="font-mono text-muted-foreground tabular">{p.stickers.length}</span>
            <Moves
              name={p.title}
              at={i}
              of={value.length}
              onMove={(step) => onChange(moved(value, i, step))}
              onRemove={remove}
            />
          </span>
        );
      })}
      {others.length > 0 && (
        <Menu>
          <MenuTrigger render={<Button size="xs" variant="outline" />}>
            <Plus /> Add pack
          </MenuTrigger>
          <MenuPopup align="start">
            {others.map((p) => (
              <MenuItem key={p.id} onClick={() => onChange([...value, p.id])}>
                <span className="min-w-0 flex-1 truncate">{p.title}</span>
                <span className="font-mono text-muted-foreground text-xs tabular">
                  {p.stickers.length} {p.stickers.length === 1 ? "sticker" : "stickers"}
                </span>
              </MenuItem>
            ))}
          </MenuPopup>
        </Menu>
      )}
      {packs.length === 0 && <ToMedia>No packs yet. Make them</ToMedia>}
    </Chosen>
  );
}

/** useWide reports whether the window is at least 40rem wide: then stickers
 * are added in a popover, else in a sheet from the bottom. */
function useWide(): boolean {
  return useSyncExternalStore(
    (changed) => {
      const query = window.matchMedia("(min-width: 40rem)");
      query.addEventListener("change", changed);
      return () => query.removeEventListener("change", changed);
    },
    () => window.matchMedia("(min-width: 40rem)").matches,
  );
}

/** StickerPicker chooses single stickers, in order; Add opens a grid of
 * the stickers kept, by pack. */
function StickerPicker({
  labelling,
  value,
  onChange,
  stickers,
  packs,
}: {
  labelling: Labelling;
  value: number[];
  onChange: (value: number[]) => void;
  stickers: Sticker[];
  packs: StickerPack[];
}) {
  const wide = useWide();
  const [open, setOpen] = useState(false);
  const byID = new Map(stickers.map((s) => [s.id, s]));
  const trigger = (
    <Button size="xs" variant="outline">
      <Plus /> Add stickers
    </Button>
  );
  const grid = <AddGrid value={value} onChange={onChange} stickers={stickers} packs={packs} />;
  return (
    <Chosen labelling={labelling}>
      {value.map((id, i) => {
        const s = byID.get(id);
        const remove = () => onChange(value.filter((v) => v !== id));
        if (!s) {
          return <Gone key={id} name={`#${id}`} onRemove={remove} />;
        }
        return (
          <span key={id} className={chip} title={s.text || "No text: agents can't see it"}>
            <span className="surface-inset flex size-6 flex-none items-center justify-center overflow-hidden rounded">
              <img src={pictureURL(id)} alt="" loading="lazy" className="max-h-full max-w-full object-contain" />
            </span>
            <span className="font-mono tabular">{id}</span>
            {!s.text && <CircleAlert className="size-3 flex-none text-warning-foreground" aria-label="no text" />}
            <Moves
              name={`sticker ${id}`}
              at={i}
              of={value.length}
              onMove={(step) => onChange(moved(value, i, step))}
              onRemove={remove}
            />
          </span>
        );
      })}
      {stickers.length === 0 ? (
        <ToMedia>No stickers yet. Keep them</ToMedia>
      ) : wide ? (
        <Popover open={open} onOpenChange={setOpen}>
          <PopoverTrigger render={trigger} />
          <PopoverPopup className="w-[min(36rem,calc(100vw-2rem))]">
            <div className="flex items-center justify-between gap-2 border-b px-3 py-2">
              <PopoverTitle>Add stickers</PopoverTitle>
              <PopoverClose render={<Button size="xs" variant="ghost" />}>Done</PopoverClose>
            </div>
            {grid}
          </PopoverPopup>
        </Popover>
      ) : (
        <>
          <Button size="xs" variant="outline" onClick={() => setOpen(true)}>
            <Plus /> Add stickers
          </Button>
          <Sheet open={open} onOpenChange={setOpen}>
            <SheetPopup side="bottom" className="max-h-[85dvh]">
              <SheetHeader>
                <SheetTitle>Add stickers</SheetTitle>
              </SheetHeader>
              <SheetPanel>{grid}</SheetPanel>
            </SheetPopup>
          </Sheet>
        </>
      )}
    </Chosen>
  );
}

/** AddGrid shows the stickers kept, all or one pack's or those in none,
 * as a toggle group of many: a press adds a sticker at the end, another
 * removes it. Those the view hides keep how they are. */
function AddGrid({
  value,
  onChange,
  stickers,
  packs,
}: {
  value: number[];
  onChange: (value: number[]) => void;
  stickers: Sticker[];
  packs: StickerPack[];
}) {
  const [view, setView] = useState(allStickers);
  const shown = stickers.filter((s) => inView(s.packs, view));
  const visible = new Set(shown.map((s) => s.id));
  const tab =
    "flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-lg px-2 py-0.5 text-muted-foreground text-xs outline-none transition-colors hover:bg-accent/50 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring data-pressed:bg-accent data-pressed:font-medium data-pressed:text-foreground";
  return (
    <div className="flex min-h-0 flex-col gap-2 p-3">
      <ToggleGroup
        aria-label="Packs"
        className="flex gap-1 overflow-x-auto pb-0.5 [scrollbar-width:thin]"
        value={[view]}
        // A second press on the picked view keeps it.
        onValueChange={(v) => v[0] && setView(v[0])}
      >
        <Toggle value={allStickers} className={tab}>
          All
        </Toggle>
        {packs.map((p) => (
          <Toggle key={p.id} value={p.id} className={tab}>
            {p.title}
          </Toggle>
        ))}
        <Toggle value={noPack} className={tab}>
          Not in a pack
        </Toggle>
      </ToggleGroup>
      <ToggleGroup
        multiple
        aria-label="Stickers"
        className="grid max-h-[min(24rem,50dvh)] grid-cols-[repeat(auto-fill,minmax(4.5rem,1fr))] gap-1.5 overflow-y-auto"
        value={value.filter((id) => visible.has(id)).map(String)}
        onValueChange={(pressed) => {
          const on = new Set(pressed.map(Number));
          const kept = value.filter((id) => !visible.has(id) || on.has(id));
          onChange([...kept, ...shown.map((s) => s.id).filter((id) => on.has(id) && !kept.includes(id))]);
        }}
      >
        {shown.map((s) => (
          <Toggle
            key={s.id}
            value={String(s.id)}
            aria-label={`Sticker ${s.id}${s.text ? `: ${s.text}` : ", no text: agents can't see it"}`}
            className="group relative flex flex-col gap-1 rounded-lg border border-transparent p-1 outline-none transition-colors hover:bg-accent/50 focus-visible:ring-2 focus-visible:ring-ring data-pressed:border-ring data-pressed:bg-accent"
          >
            <span className="surface-inset flex aspect-square items-center justify-center overflow-hidden rounded-md">
              <img src={pictureURL(s.id)} alt="" loading="lazy" className="max-h-full max-w-full object-contain" />
            </span>
            <span className="absolute top-1.5 left-1.5 rounded bg-background/88 px-1 font-mono text-xs tabular leading-4 shadow-xs/5">
              {s.id}
            </span>
            <span className="absolute top-1.5 right-1.5 hidden size-4 items-center justify-center rounded-full bg-foreground text-background group-data-pressed:flex">
              <Check className="size-3" />
            </span>
            {!s.text && <span className="px-0.5 text-warning-foreground text-xs leading-4">No text</span>}
          </Toggle>
        ))}
        {shown.length === 0 && (
          <p className="col-span-full py-6 text-center text-muted-foreground text-sm">No stickers here.</p>
        )}
      </ToggleGroup>
    </div>
  );
}

/** StickersSeen is the line under an agent's pickers: how many stickers it
 * sees of those it is given, and, past its max_stickers, which. Nothing
 * while the stickers cannot be read or none are chosen. */
export function StickersSeen({
  agent,
  stickers,
  packs,
  max,
}: {
  agent: string;
  stickers: unknown;
  packs: unknown;
  max: number;
}) {
  const { data } = useStickers();
  const chosen = (Array.isArray(stickers) && stickers.length > 0) || (Array.isArray(packs) && packs.length > 0);
  if (!data || !chosen) {
    return null;
  }
  const { given } = shownTo(stickers, packs, max, data.stickers, data.packs);
  const note = seenNote(agent, given, max);
  return <span className={cn(note.over && "text-warning-foreground")}>{note.text}</span>;
}
