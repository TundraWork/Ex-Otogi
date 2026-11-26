import { Toggle } from "@base-ui/react/toggle";
import { ToggleGroup } from "@base-ui/react/toggle-group";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useBlocker } from "@tanstack/react-router";
import { format } from "date-fns";
import { ImagePlus, Plus, ScrollText, Trash2, Upload, X } from "lucide-react";
import {
  type DragEvent,
  type KeyboardEvent,
  type ReactNode,
  type RefObject,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { ApiError } from "@/api/client";
import { Card, CardHeader } from "@/components/card";
import { LoadError } from "@/components/load-error";
import { Notice } from "@/components/notice";
import { Picture } from "@/components/picture";
import { SearchInput } from "@/components/search-input";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogPopup,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Menu, MenuItem, MenuPopup, MenuTrigger } from "@/components/ui/menu";
import { Sheet, SheetHeader, SheetPanel, SheetPopup, SheetTitle } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { Textarea } from "@/components/ui/textarea";
import { toastManager } from "@/components/ui/toast";
import {
  type Agent,
  accepted,
  allStickers,
  givenTo,
  inView,
  matches,
  next,
  noPack,
  onlyIn,
  packIDProblem,
  sentQuery,
  size,
  usage,
} from "@/lib/stickers";
import { cn } from "@/lib/utils";
import { modulesQuery, settingsQuery } from "@/queries/modules";
import { usePeopleNames } from "@/queries/people";
import {
  editPack,
  makePack,
  noteSticker,
  packSticker,
  pictureURL,
  removePack,
  removeSticker,
  type Sticker,
  type StickerPack,
  stickersQuery,
  unpackSticker,
  uploadSticker,
} from "@/queries/stickers";

/** wideFrom is how wide the card must be, in px, for the details to stand
 * beside the grid rather than in a sheet: 56rem. */
const wideFrom = 56 * 16;

/** useWidth is an element's width, kept as it changes. */
function useWidth(ref: RefObject<HTMLElement | null>): number {
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el) {
      return;
    }
    const observer = new ResizeObserver(([entry]) => setWidth(entry?.contentRect.width ?? 0));
    observer.observe(el);
    return () => observer.disconnect();
  }, [ref]);
  return width;
}

/** Pending is a picture on its way in: shown at the grid's end while the
 * module describes it, or with why it was refused. */
type Pending = { key: string; name: string; preview?: string; refused?: string };

/** PackEdit is the pack dialog's state: a new pack, or the pack edited. */
type PackEdit = { pack?: StickerPack } | undefined;

/** StickersCard manages the media module's stickers, the pictures agents
 * may send, each with the note that tells agents when to, and their packs,
 * which give agents stickers together. */
export function StickersCard() {
  const { data: modules } = useQuery(modulesQuery);
  const running = modules?.find((m) => m.id === "media")?.running ?? false;
  const list = useQuery({ ...stickersQuery, enabled: running });
  const chat = useQuery({ ...settingsQuery({ module: "chat" }), retry: false });
  const packs = useMemo(() => list.data?.packs ?? [], [list.data]);
  const given = useMemo(() => givenTo(chat.data?.settings, packs), [chat.data, packs]);
  const queryClient = useQueryClient();

  const box = useRef<HTMLDivElement>(null);
  const wide = useWidth(box) >= wideFrom;
  const [selected, setSelected] = useState<number>();
  const [sheetOpen, setSheetOpen] = useState(false);
  const [drafts, setDrafts] = useState<Record<number, string>>({});
  const [uploads, setUploads] = useState<Pending[]>([]);
  const [uploading, setUploading] = useState(false);
  const [dragging, setDragging] = useState(false);
  const [view, setView] = useState(allStickers);
  const [filter, setFilter] = useState("");
  const [packEdit, setPackEdit] = useState<PackEdit>();
  const [packRemoving, setPackRemoving] = useState<StickerPack>();
  const noteRef = useRef<HTMLTextAreaElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const stickers = list.data?.stickers ?? [];
  const current = stickers.find((s) => s.id === selected);
  const pack = packs.find((p) => p.id === view);
  // A pack removed elsewhere leaves its view for all stickers.
  const shownView = view === allStickers || view === noPack || pack ? view : allStickers;
  const shown = stickers.filter((s) => inView(s.packs, shownView) && matches(s, filter));
  // Drafts of stickers removed meanwhile, here or elsewhere, leave nothing
  // to save.
  const dirty = stickers.some((s) => drafts[s.id] !== undefined);

  const select = (id: number | undefined) => {
    setSelected(id);
    setSheetOpen(id !== undefined && !wide);
  };

  // Every file goes to the media module, which tells pictures by their
  // content, not by the name the browser guesses a type from: one it does
  // not keep stays at the grid's end with its reason.
  const keep = async (files: File[]) => {
    if (files.length === 0) {
      return;
    }
    const into = pack;
    const batch = files.map((f, i) => ({
      file: f,
      upload: {
        key: `${Date.now()}-${i}-${f.name}`,
        name: f.name,
        preview: f.type.startsWith("image/") ? URL.createObjectURL(f) : undefined,
      },
    }));
    setUploads((u) => [...u, ...batch.map((b) => b.upload)]);
    setUploading(true);
    let first = selected === undefined;
    for (const { file, upload } of batch) {
      try {
        const st = await uploadSticker(file, into?.id ?? "");
        if (upload.preview) {
          URL.revokeObjectURL(upload.preview);
        }
        setUploads((u) => u.filter((x) => x.key !== upload.key));
        await queryClient.invalidateQueries({ queryKey: stickersQuery.queryKey });
        if (st.existing) {
          toastManager.add({ type: "success", title: `Sticker ${st.id} was kept already; added to ${into?.title}` });
        } else if (first) {
          first = false;
          select(st.id);
          requestAnimationFrame(() => noteRef.current?.focus());
        }
      } catch (err) {
        const refused = err instanceof Error ? err.message : String(err);
        setUploads((u) => u.map((x) => (x.key === upload.key ? { ...x, refused } : x)));
      }
    }
    setUploading(false);
  };

  const leaving = useBlocker({
    shouldBlockFn: ({ current: here, next: there }) => (dirty || uploading) && here.pathname !== there.pathname,
    enableBeforeUnload: () => dirty || uploading,
    withResolver: true,
  });

  const onDrop = (e: DragEvent) => {
    e.preventDefault();
    setDragging(false);
    if (running) {
      void keep([...e.dataTransfer.files]);
    }
  };

  if (!modules) {
    return null;
  }
  const detail = current && (
    <Detail
      key={current.id}
      sticker={current}
      packs={packs}
      agents={chat.data ? (given.get(current.id) ?? []) : undefined}
      draft={drafts[current.id]}
      setDraft={(note) =>
        setDrafts(({ [current.id]: _, ...rest }) => (note === undefined ? rest : { ...rest, [current.id]: note }))
      }
      noteRef={noteRef}
      onRemoved={() => {
        const after = next(
          shown.map((s) => s.id),
          current.id,
        );
        setDrafts(({ [current.id]: _, ...rest }) => rest);
        select(after);
      }}
    />
  );

  return (
    <Card
      className={cn("relative", dragging && "outline-2 outline-ring outline-dashed outline-offset-2")}
      onDragOver={(e) => {
        if (running && e.dataTransfer.types.includes("Files")) {
          e.preventDefault();
          setDragging(true);
        }
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) {
          setDragging(false);
        }
      }}
      onDrop={onDrop}
    >
      <CardHeader
        title="Stickers"
        note={list.data ? usage(stickers) : undefined}
        description="Pictures agents may send. An agent sees each by its note, else its description; packs give agents stickers together."
      >
        {running && list.data && (
          <Button size="sm" onClick={() => fileRef.current?.click()}>
            <Upload /> {pack ? `Upload to ${pack.title}` : "Upload"}
          </Button>
        )}
      </CardHeader>
      <input
        ref={fileRef}
        type="file"
        multiple
        accept={accepted.join(",")}
        className="hidden"
        onChange={(e) => {
          void keep([...(e.target.files ?? [])]);
          e.target.value = "";
        }}
      />
      {dragging && (
        <div className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center rounded-2xl bg-background/72 font-medium text-sm">
          {pack ? `Drop to upload to ${pack.title}` : "Drop to upload"}
        </div>
      )}
      <div ref={box} className="mx-3.5 mb-3.5">
        {!running ? (
          <Notice tone="warning" title="The media module isn't running, so its stickers can't be shown">
            <p className="mt-0.5">Turn it on at the top of this page.</p>
          </Notice>
        ) : list.error && !list.data ? (
          <LoadError what="the stickers" error={list.error} retry={() => void list.refetch()} />
        ) : (
          <>
            {list.data && (
              <PackBar
                stickers={stickers}
                packs={packs}
                view={shownView}
                onView={setView}
                pack={pack}
                filter={filter}
                onFilter={setFilter}
                onNew={() => setPackEdit({})}
                onEdit={() => setPackEdit({ pack })}
                onRemove={() => setPackRemoving(pack)}
              />
            )}
            <div className={cn("grid items-start gap-3", wide && "grid-cols-[minmax(0,1fr)_20rem]")}>
              <Grid
                stickers={list.data ? shown : undefined}
                uploads={uploads}
                given={given}
                selected={selected}
                drafts={drafts}
                // A second press on the picked one keeps it picked, and opens
                // its sheet again on a narrow card.
                onSelect={(id) => select(id ?? selected)}
                onDismiss={(key) => setUploads((u) => u.filter((x) => x.key !== key))}
                empty={
                  stickers.length === 0 ? (
                    <Empty>
                      <p className="max-w-md text-muted-foreground text-sm">
                        No stickers yet. Upload PNG, JPEG, or GIF pictures, or reply to a picture in a chat with{" "}
                        <code className="font-mono text-foreground text-xs">/sticker add</code>.
                      </p>
                      <Button size="sm" onClick={() => fileRef.current?.click()}>
                        <Upload /> Upload
                      </Button>
                    </Empty>
                  ) : filter.trim() ? (
                    <Empty>
                      <p className="text-muted-foreground text-sm">No stickers match.</p>
                    </Empty>
                  ) : pack ? (
                    <Empty>
                      <p className="max-w-md text-muted-foreground text-sm">
                        No stickers in this pack yet. Upload into it, or add stickers from All with Add to pack.
                      </p>
                    </Empty>
                  ) : (
                    <Empty>
                      <p className="text-muted-foreground text-sm">Every sticker is in a pack.</p>
                    </Empty>
                  )
                }
              />
              {wide && (
                <aside className="surface-inset sticky top-4 rounded-xl p-3">
                  {detail ?? (
                    <p className="py-8 text-center text-muted-foreground text-sm">
                      Pick a sticker to see it larger, write its note, or remove it.
                    </p>
                  )}
                </aside>
              )}
            </div>
          </>
        )}
      </div>
      {!wide && (
        <Sheet open={sheetOpen && current !== undefined} onOpenChange={setSheetOpen}>
          <SheetPopup side="bottom" className="max-h-[85dvh]">
            <SheetHeader>
              <SheetTitle>Sticker {current?.id}</SheetTitle>
            </SheetHeader>
            <SheetPanel>{detail}</SheetPanel>
          </SheetPopup>
        </Sheet>
      )}
      <PackDialog
        edit={packEdit}
        taken={packs.map((p) => p.id)}
        onClose={() => setPackEdit(undefined)}
        onMade={(id) => setView(id)}
      />
      <RemovePackDialog
        pack={packRemoving}
        stickers={stickers}
        agents={chat.data ? agentsOfPack(chat.data.settings, packRemoving?.id) : undefined}
        onClose={() => setPackRemoving(undefined)}
        onRemoved={() => setView(allStickers)}
      />
      <Dialog open={leaving.status === "blocked"} onOpenChange={(open) => !open && leaving.reset?.()}>
        <DialogPopup className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Leave this page?</DialogTitle>
            <DialogDescription>
              {uploading
                ? "Pictures are still uploading, and leaving stops them."
                : "Some stickers have notes not saved, and leaving discards them."}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <DialogClose render={<Button variant="ghost" />}>Stay</DialogClose>
            <Button variant="destructive" onClick={() => leaving.proceed?.()}>
              Leave
            </Button>
          </DialogFooter>
        </DialogPopup>
      </Dialog>
    </Card>
  );
}

/** agentsOfPack is the titles of the agents whose settings name pack. */
function agentsOfPack(chatSettings: unknown, pack: string | undefined): string[] {
  const profiles = (chatSettings as { profiles?: unknown } | undefined)?.profiles;
  if (!pack || !Array.isArray(profiles)) {
    return [];
  }
  return profiles
    .filter((p) => Array.isArray(p?.media?.packs) && p.media.packs.includes(pack))
    .map((p) => (typeof p.label === "string" && p.label ? p.label : String(p.name)));
}

/** Empty fills the grid's place when it has nothing to show. */
function Empty({ children }: { children: ReactNode }) {
  return (
    <div className="surface-inset col-span-full flex flex-col items-center gap-3 rounded-xl px-4 py-10 text-center">
      <ImagePlus className="size-6 text-muted-foreground" />
      {children}
    </div>
  );
}

/** PackBar picks which stickers the grid shows: all, one pack's, or those
 * in none; with a pack picked, its description and what can be done to it.
 * The packs are a single-choice toggle group, like the grid. */
function PackBar({
  stickers,
  packs,
  view,
  onView,
  pack,
  filter,
  onFilter,
  onNew,
  onEdit,
  onRemove,
}: {
  stickers: Sticker[];
  packs: StickerPack[];
  view: string;
  onView: (view: string) => void;
  pack: StickerPack | undefined;
  filter: string;
  onFilter: (filter: string) => void;
  onNew: () => void;
  onEdit: () => void;
  onRemove: () => void;
}) {
  // With nothing kept and no pack, only New pack is offered.
  const any = stickers.length > 0 || packs.length > 0;
  const chip =
    "flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-lg px-2.5 py-1 text-sm text-muted-foreground outline-none transition-colors hover:bg-accent/50 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring data-pressed:bg-accent data-pressed:font-medium data-pressed:text-foreground";
  const count = "font-mono text-muted-foreground text-xs tabular";
  return (
    <div className="mb-3 flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        {any && (
          <ToggleGroup
            aria-label="Packs"
            className="flex min-w-0 flex-1 gap-1 overflow-x-auto pb-0.5 [scrollbar-width:thin] max-sm:basis-full"
            value={[view]}
            // A second press on the picked view keeps it.
            onValueChange={(value) => value[0] && onView(value[0])}
          >
            <Toggle value={allStickers} className={chip}>
              All <span className={count}>{stickers.length}</span>
            </Toggle>
            {packs.map((p) => (
              <Toggle key={p.id} value={p.id} className={chip}>
                {p.title} <span className={count}>{p.stickers.length}</span>
              </Toggle>
            ))}
            <Toggle value={noPack} className={chip}>
              Not in a pack <span className={count}>{stickers.filter((s) => s.packs.length === 0).length}</span>
            </Toggle>
          </ToggleGroup>
        )}
        <Button size="sm" variant="outline" className={cn(!any && "ms-auto")} onClick={onNew}>
          <Plus /> New pack
        </Button>
        {any && (
          <SearchInput
            size="sm"
            className="w-40 max-sm:min-w-0 max-sm:flex-1"
            placeholder="Filter"
            aria-label="Filter stickers by number or text"
            value={filter}
            onChange={(e) => onFilter(e.target.value)}
          />
        )}
      </div>
      {pack && (
        <div className="flex items-center gap-2">
          <p className="min-w-0 flex-1 truncate text-muted-foreground text-sm">
            <span className="font-mono text-xs">{pack.id}</span>
            {pack.description ? ` · ${pack.description}` : ""}
          </p>
          <Button size="xs" variant="ghost" onClick={onEdit}>
            Edit
          </Button>
          <Button size="xs" variant="destructive-outline" onClick={onRemove}>
            Remove pack
          </Button>
        </div>
      )}
    </div>
  );
}

/** Grid shows the stickers, one cell each, and the uploads after them. It
 * is a single-choice toggle group: Tab stops on it once, the arrow keys
 * move along it, and Enter or Space picks. A press on the picked one gives
 * onSelect nothing. */
function Grid({
  stickers,
  uploads,
  given,
  selected,
  drafts,
  onSelect,
  onDismiss,
  empty,
}: {
  stickers: Sticker[] | undefined;
  uploads: Pending[];
  given: Map<number, Agent[]>;
  selected: number | undefined;
  drafts: Record<number, string>;
  onSelect: (id: number | undefined) => void;
  onDismiss: (key: string) => void;
  empty: ReactNode;
}) {
  const cells = "grid grid-cols-[repeat(auto-fill,minmax(8.5rem,1fr))] gap-2";
  if (!stickers) {
    return (
      <div className={cells}>
        {Array.from({ length: 6 }, (_, i) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: placeholders have nothing else to tell them apart
          <Skeleton key={i} className="aspect-[4/5] rounded-xl" />
        ))}
      </div>
    );
  }
  if (stickers.length === 0 && uploads.length === 0) {
    return <div className={cells}>{empty}</div>;
  }
  return (
    <ToggleGroup
      aria-label="Stickers"
      className={cells}
      value={selected === undefined ? [] : [String(selected)]}
      onValueChange={(value) => onSelect(value[0] === undefined ? undefined : Number(value[0]))}
    >
      {stickers.map((s) => (
        <Toggle
          key={s.id}
          value={String(s.id)}
          aria-label={`Sticker ${s.id}${s.text ? `: ${s.text}` : ", no text"}`}
          className="group relative flex flex-col gap-1.5 rounded-xl border border-transparent p-1.5 text-left outline-none transition-colors hover:bg-accent/50 focus-visible:ring-2 focus-visible:ring-ring data-pressed:border-ring data-pressed:bg-accent data-pressed:ring-1 data-pressed:ring-ring"
        >
          <Thumbnail id={s.id} />
          <span className="absolute top-2.5 left-2.5 rounded-md bg-background/88 px-1.5 font-mono text-xs tabular shadow-xs/5">
            {s.id}
          </span>
          {drafts[s.id] !== undefined && (
            <span
              className="absolute top-2.5 right-2.5 size-2 rounded-full bg-warning ring-2 ring-background"
              title="Note not saved"
            />
          )}
          <span
            className={cn(
              "line-clamp-2 min-h-[2lh] px-0.5 text-xs leading-snug",
              s.note ? "text-foreground" : s.text ? "text-muted-foreground" : "text-warning-foreground",
            )}
          >
            {s.text || "No text — agents can't see it"}
          </span>
          {(given.get(s.id)?.length ?? 0) > 0 && (
            <span className="flex flex-wrap gap-1 px-0.5">
              {given.get(s.id)?.map((a) => (
                <span key={a.name} className="rounded-full bg-secondary px-1.5 text-secondary-foreground text-xs">
                  {a.title}
                </span>
              ))}
            </span>
          )}
        </Toggle>
      ))}
      {uploads.map((u) => (
        <UploadCell key={u.key} upload={u} onDismiss={() => onDismiss(u.key)} />
      ))}
    </ToggleGroup>
  );
}

/** Thumbnail is a sticker's picture in a square, or why it could not load. */
function Thumbnail({ id }: { id: number }) {
  const [failed, setFailed] = useState(false);
  return (
    <span className="surface-inset flex aspect-square items-center justify-center overflow-hidden rounded-lg">
      {failed ? (
        <span className="px-2 text-center text-muted-foreground text-xs">Couldn't load</span>
      ) : (
        <img
          src={pictureURL(id)}
          alt=""
          loading="lazy"
          onError={() => setFailed(true)}
          className="max-h-full max-w-full object-contain"
        />
      )}
    </span>
  );
}

/** LargePicture is a sticker's picture in the details, or, when it does not
 * load, why: an <img> does not say, so it is asked for again, as the chat
 * page's media does. */
function LargePicture({ id }: { id: number }) {
  const [failed, setFailed] = useState(false);
  const reason = useQuery({
    queryKey: ["sticker-picture-failure", id],
    enabled: failed,
    staleTime: Number.POSITIVE_INFINITY,
    queryFn: async () => {
      const response = await fetch(pictureURL(id));
      return response.ok ? "" : new ApiError(response.status, await response.json().catch(() => undefined)).message;
    },
  });
  if (failed) {
    return (
      <span className="px-3 py-8 text-center text-muted-foreground text-sm">
        {`The picture could not be loaded${reason.data ? `: ${reason.data}` : "."}`}
      </span>
    );
  }
  return (
    <img
      src={pictureURL(id)}
      alt=""
      onError={() => setFailed(true)}
      className="max-h-[calc(40vh-1rem)] max-w-full object-contain"
    />
  );
}

/** UploadCell is a picture being kept: its preview while the module
 * describes it, or why it was refused. */
function UploadCell({ upload, onDismiss }: { upload: Pending; onDismiss: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    // A block, not an expression: scrollIntoView returns a promise in newer
    // browsers, and React would call what the effect returns on unmount.
    ref.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, []);
  return (
    <div ref={ref} className="flex flex-col gap-1.5 rounded-xl border border-dashed p-1.5">
      <span className="surface-inset relative flex aspect-square items-center justify-center overflow-hidden rounded-lg">
        {upload.preview ? (
          <img
            src={upload.preview}
            alt=""
            className={cn("max-h-full max-w-full object-contain", upload.refused && "opacity-40")}
          />
        ) : (
          <span className="break-all px-2 text-center font-mono text-muted-foreground text-xs">{upload.name}</span>
        )}
        {!upload.refused && (
          <span className="absolute inset-x-2 bottom-2 h-1 overflow-hidden rounded-full bg-background/72">
            <span className="block h-full w-full animate-pulse rounded-full bg-primary/64 motion-reduce:animate-none" />
          </span>
        )}
      </span>
      {upload.refused ? (
        <span className="flex items-start gap-1 px-0.5">
          <span role="alert" className="min-w-0 flex-1 text-destructive-foreground text-xs leading-snug">
            {upload.refused}
          </span>
          <Button variant="ghost" size="icon-xs" aria-label={`Dismiss ${upload.name}`} onClick={onDismiss}>
            <X />
          </Button>
        </span>
      ) : (
        <span className="px-0.5 text-muted-foreground text-xs" aria-live="polite">
          Describing…
        </span>
      )}
    </div>
  );
}

/** Detail is one sticker: larger, with what is known of it, its note to
 * write, its packs, the agents given it, and a way to remove it. agents is
 * undefined when chat's settings cannot be read. */
function Detail({
  sticker: s,
  packs,
  agents,
  draft,
  setDraft,
  noteRef,
  onRemoved,
}: {
  sticker: Sticker;
  packs: StickerPack[];
  agents: Agent[] | undefined;
  draft: string | undefined;
  setDraft: (note: string | undefined) => void;
  noteRef: RefObject<HTMLTextAreaElement | null>;
  onRemoved: () => void;
}) {
  const queryClient = useQueryClient();
  const refresh = () => queryClient.invalidateQueries({ queryKey: stickersQuery.queryKey });
  const note = draft ?? s.note;
  const changed = draft !== undefined && draft !== s.note;
  const save = useMutation({
    mutationFn: () => noteSticker(s.id, note),
    onSuccess: () => {
      setDraft(undefined);
      toastManager.add({ type: "success", title: `Sticker ${s.id}'s note saved` });
    },
    onSettled: refresh,
  });
  const remove = useMutation({
    mutationFn: () => removeSticker(s.id),
    onSuccess: () => {
      toastManager.add({ type: "success", title: `Removed sticker ${s.id}` });
      onRemoved();
    },
    onSettled: refresh,
  });
  const titleOf = (id: string) => packs.find((p) => p.id === id)?.title ?? id;
  const member = useMutation({
    mutationFn: ({ pack, into }: { pack: string; into: boolean }) =>
      into ? packSticker(pack, s.id) : unpackSticker(pack, s.id),
    onSuccess: (_, { pack, into }) =>
      toastManager.add({
        type: "success",
        title: into ? `Added sticker ${s.id} to ${titleOf(pack)}` : `Took sticker ${s.id} out of ${titleOf(pack)}`,
      }),
    onError: (err) =>
      toastManager.add({ type: "error", title: "The pack wasn't changed", description: (err as Error).message }),
    onSettled: refresh,
  });
  const onKey = (e: KeyboardEvent) => {
    if ((e.ctrlKey || e.metaKey) && e.key === "Enter" && changed) {
      e.preventDefault();
      save.mutate();
    }
  };
  const animated = s.media_type === "image/gif";
  const others = packs.filter((p) => !s.packs.includes(p.id));
  const keptBy = usePeopleNames([s.added_by]);
  return (
    <div className="flex flex-col gap-3">
      <Dialog>
        <DialogTrigger
          className="surface-raised flex max-h-[40vh] cursor-zoom-in items-center justify-center overflow-hidden rounded-lg p-2 outline-none focus-visible:ring-2 focus-visible:ring-ring"
          aria-label={`Show sticker ${s.id} larger`}
        >
          <LargePicture id={s.id} />
        </DialogTrigger>
        <Picture src={pictureURL(s.id)} />
      </Dialog>
      <div className="text-muted-foreground text-xs">
        <div className="text-foreground">
          <span className="font-mono tabular">#{s.id}</span> · {animated ? "animated" : "static"} · {size(s.bytes)} ·{" "}
          {s.media_type.replace("image/", "").toUpperCase()}
        </div>
        <div>
          Kept by {keptBy.get(s.added_by) ?? s.added_by} · {format(new Date(s.added_at), "MMM d")}
        </div>
      </div>
      <div className="flex flex-col gap-1.5">
        <label htmlFor={`sticker-note-${s.id}`} className="font-medium text-sm">
          Note
        </label>
        <Textarea
          id={`sticker-note-${s.id}`}
          ref={noteRef}
          value={note}
          placeholder={s.summary || "What it shows, and when to send it"}
          onChange={(e) => setDraft(e.target.value === s.note ? undefined : e.target.value)}
          onKeyDown={onKey}
          rows={2}
          className="[&_textarea]:field-sizing-content [&_textarea]:max-h-40"
          aria-invalid={save.isError || undefined}
        />
      </div>
      {save.error ? (
        <p className="text-destructive-foreground text-xs">{save.error.message}</p>
      ) : (
        !note &&
        !s.summary && (
          <p className="text-warning-foreground text-xs">Agents can't see this sticker until it has a note.</p>
        )
      )}
      <div className="flex gap-2">
        <Button size="sm" disabled={!changed} loading={save.isPending} onClick={() => save.mutate()}>
          Save
        </Button>
        <Button size="sm" variant="ghost" disabled={draft === undefined} onClick={() => setDraft(undefined)}>
          Revert
        </Button>
      </div>
      <div className="flex flex-col gap-1.5">
        <div className="font-medium text-sm">Packs</div>
        <div className="flex flex-wrap items-center gap-1">
          {s.packs.length === 0 && <span className="text-muted-foreground text-xs">Not in a pack</span>}
          {s.packs.map((id) => (
            <span
              key={id}
              className="inline-flex items-center gap-0.5 rounded-full bg-secondary py-0.5 ps-2 pe-0.5 text-secondary-foreground text-xs"
            >
              {titleOf(id)}
              <Button
                variant="ghost"
                size="icon-xs"
                className="size-5 rounded-full"
                aria-label={`Take sticker ${s.id} out of ${titleOf(id)}`}
                disabled={member.isPending}
                onClick={() => member.mutate({ pack: id, into: false })}
              >
                <X />
              </Button>
            </span>
          ))}
          {others.length > 0 && (
            <Menu>
              <MenuTrigger
                render={
                  <Button size="xs" variant="ghost" disabled={member.isPending} className="text-muted-foreground" />
                }
              >
                <Plus /> Add to pack
              </MenuTrigger>
              <MenuPopup align="start">
                {others.map((p) => (
                  <MenuItem key={p.id} onClick={() => member.mutate({ pack: p.id, into: true })}>
                    {p.title}
                  </MenuItem>
                ))}
              </MenuPopup>
            </Menu>
          )}
        </div>
      </div>
      {s.summary && (
        <div className="text-xs">
          <div className="font-medium text-foreground text-sm">Described as</div>
          <Summary text={s.summary} />
        </div>
      )}
      {agents && (
        <div className="flex flex-col gap-1.5">
          <div className="font-medium text-sm">Given to</div>
          {agents.length > 0 ? (
            <div className="flex flex-wrap gap-1">
              {agents.map((a) => (
                <span key={a.name} className="rounded-full bg-secondary px-2 py-0.5 text-secondary-foreground text-xs">
                  {a.title}
                  {a.via && <span className="text-muted-foreground"> · via {titleOf(a.via)}</span>}
                </span>
              ))}
            </div>
          ) : (
            <p className="text-muted-foreground text-xs">Not given to any agent</p>
          )}
          <Link
            to="/modules/$module"
            params={{ module: "chat" }}
            className="w-fit text-xs underline underline-offset-3"
          >
            Change in chat settings
          </Link>
        </div>
      )}
      <Link
        to="/events"
        search={{ view: "records", kind: "chat.reply", q: sentQuery(s.id) }}
        className="flex w-fit items-center gap-1 text-xs underline underline-offset-3"
      >
        <ScrollText className="size-3.5" /> Sent by agents
      </Link>
      <Dialog>
        <DialogTrigger render={<Button variant="destructive-outline" size="sm" className="w-fit" />}>
          <Trash2 /> Remove
        </DialogTrigger>
        <DialogPopup className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Remove sticker {s.id}?</DialogTitle>
            <DialogDescription>
              Its number is not used again.
              {s.packs.length > 0 ? ` It leaves ${s.packs.map(titleOf).join(", ")}.` : ""}
              {agents?.some((a) => !a.via)
                ? ` ${agents
                    .filter((a) => !a.via)
                    .map((a) => a.title)
                    .join(", ")} still list its number in chat settings; take it out there.`
                : ""}
            </DialogDescription>
          </DialogHeader>
          {remove.error && <p className="px-6 text-destructive-foreground text-sm">{remove.error.message}</p>}
          <DialogFooter>
            <DialogClose render={<Button variant="ghost" />}>Keep it</DialogClose>
            <Button variant="destructive" loading={remove.isPending} onClick={() => remove.mutate()}>
              Remove
            </Button>
          </DialogFooter>
        </DialogPopup>
      </Dialog>
    </div>
  );
}

/** Summary is a description, cut at three lines until opened. */
function Summary({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLParagraphElement>(null);
  const [long, setLong] = useState(false);
  useEffect(() => {
    const el = ref.current;
    if (el) {
      setLong(el.scrollHeight > el.clientHeight + 1);
    }
  }, []);
  return (
    <>
      <p ref={ref} className={cn("mt-1 text-muted-foreground leading-relaxed", !open && "line-clamp-3")}>
        {text}
      </p>
      {(long || open) && (
        <button
          type="button"
          aria-expanded={open}
          className="mt-1 rounded-sm text-xs underline underline-offset-3 outline-none focus-visible:ring-2 focus-visible:ring-ring"
          onClick={() => setOpen((o) => !o)}
        >
          {open ? "Show less" : "Show all"}
        </button>
      )}
    </>
  );
}

/** PackDialog makes a pack, or edits one: its ID only when new, as chat's
 * settings name it by its ID. */
function PackDialog({
  edit,
  taken,
  onClose,
  onMade,
}: {
  edit: PackEdit;
  taken: string[];
  onClose: () => void;
  onMade: (id: string) => void;
}) {
  const queryClient = useQueryClient();
  const editing = edit?.pack;
  const [id, setID] = useState("");
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [touched, setTouched] = useState(false);
  // Each opening is a new edit, so the fields start afresh, a new pack's
  // included.
  useEffect(() => {
    setID(edit?.pack?.id ?? "");
    setTitle(edit?.pack?.title ?? "");
    setDescription(edit?.pack?.description ?? "");
    setTouched(false);
  }, [edit]);
  const idProblem = editing ? "" : packIDProblem(id, taken);
  const titleProblem = title.trim() === "" ? "Give it a title." : "";
  const save = useMutation({
    mutationFn: () => (editing ? editPack(editing.id, title, description) : makePack({ id, title, description })),
    onSuccess: (p) => {
      toastManager.add({ type: "success", title: editing ? `${p.title} saved` : `Made ${p.title}` });
      if (!editing) {
        onMade(p.id);
      }
      onClose();
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: stickersQuery.queryKey }),
  });
  const submit = () => {
    setTouched(true);
    if (!idProblem && !titleProblem) {
      save.mutate();
    }
  };
  return (
    <Dialog
      open={edit !== undefined}
      onOpenChange={(open) => {
        if (!open) {
          save.reset();
          onClose();
        }
      }}
    >
      <DialogPopup className="sm:max-w-md">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
        >
          <DialogHeader>
            <DialogTitle>{editing ? `Edit ${editing.title}` : "New pack"}</DialogTitle>
            <DialogDescription>
              {editing
                ? "Its ID stays, so agents' settings that name it still do."
                : "Agents are given a pack by its ID in chat settings; it can't change later."}
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-3 px-6 pb-2">
            <div className="flex flex-col gap-1.5">
              <label htmlFor="pack-id" className="font-medium text-sm">
                ID
              </label>
              <Input
                id="pack-id"
                value={id}
                disabled={Boolean(editing)}
                placeholder="eku-memes"
                autoComplete="off"
                spellCheck={false}
                className="font-mono"
                aria-invalid={(touched && Boolean(idProblem)) || undefined}
                aria-describedby="pack-id-help"
                onChange={(e) => setID(e.target.value)}
              />
              <p
                id="pack-id-help"
                className={cn(
                  "text-xs",
                  touched && idProblem ? "text-destructive-foreground" : "text-muted-foreground",
                )}
              >
                {touched && idProblem
                  ? idProblem
                  : "Lowercase letters, digits, and hyphens, starting with a letter or digit, at most 32."}
              </p>
            </div>
            <div className="flex flex-col gap-1.5">
              <label htmlFor="pack-title" className="font-medium text-sm">
                Title
              </label>
              <Input
                id="pack-title"
                value={title}
                autoComplete="off"
                aria-invalid={(touched && Boolean(titleProblem)) || undefined}
                onChange={(e) => setTitle(e.target.value)}
              />
              {touched && titleProblem && <p className="text-destructive-foreground text-xs">{titleProblem}</p>}
            </div>
            <div className="flex flex-col gap-1.5">
              <label htmlFor="pack-description" className="font-medium text-sm">
                Description
              </label>
              <Textarea
                id="pack-description"
                value={description}
                rows={2}
                onChange={(e) => setDescription(e.target.value)}
              />
            </div>
            {save.error && <p className="text-destructive-foreground text-sm">{save.error.message}</p>}
          </div>
          <DialogFooter>
            <DialogClose render={<Button variant="ghost" />}>Cancel</DialogClose>
            <Button type="submit" loading={save.isPending}>
              {editing ? "Save" : "Make pack"}
            </Button>
          </DialogFooter>
        </form>
      </DialogPopup>
    </Dialog>
  );
}

/** RemovePackDialog removes a pack, keeping its stickers or removing those
 * in no other pack: two buttons, so each way reads with what it does. */
function RemovePackDialog({
  pack,
  stickers,
  agents,
  onClose,
  onRemoved,
}: {
  pack: StickerPack | undefined;
  stickers: Sticker[];
  agents: string[] | undefined;
  onClose: () => void;
  onRemoved: () => void;
}) {
  const queryClient = useQueryClient();
  const remove = useMutation({
    mutationFn: (how: "keep" | "remove") => removePack(pack?.id ?? "", how),
    onSuccess: (out) => {
      toastManager.add({
        type: "success",
        title:
          out.removed.length > 0
            ? `Removed ${pack?.title} and ${out.removed.length} stickers`
            : `Removed ${pack?.title}; its stickers stay`,
      });
      onRemoved();
      onClose();
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: stickersQuery.queryKey }),
  });
  const only = pack ? onlyIn(stickers, pack.id) : 0;
  const count = pack?.stickers.length ?? 0;
  return (
    <Dialog
      open={pack !== undefined}
      onOpenChange={(open) => {
        if (!open) {
          remove.reset();
          onClose();
        }
      }}
    >
      <DialogPopup className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Remove {pack?.title}?</DialogTitle>
          <DialogDescription>
            It holds {count} {count === 1 ? "sticker" : "stickers"}
            {count > 0 ? `, ${only} of them in no other pack` : ""}. This can't be undone.
            {agents && agents.length > 0
              ? ` ${agents.join(", ")} still ${agents.length === 1 ? "names" : "name"} it in chat settings; take it out there.`
              : ""}
          </DialogDescription>
        </DialogHeader>
        {remove.error && <p className="px-6 text-destructive-foreground text-sm">{remove.error.message}</p>}
        <DialogFooter className="flex-wrap">
          <DialogClose render={<Button variant="ghost" />}>Keep it</DialogClose>
          <Button
            variant="destructive-outline"
            loading={remove.isPending && remove.variables === "keep"}
            onClick={() => remove.mutate("keep")}
          >
            Remove pack, keep stickers
          </Button>
          {only > 0 && (
            <Button
              variant="destructive"
              loading={remove.isPending && remove.variables === "remove"}
              onClick={() => remove.mutate("remove")}
            >
              Remove pack and its {only} {only === 1 ? "sticker" : "stickers"}
            </Button>
          )}
        </DialogFooter>
      </DialogPopup>
    </Dialog>
  );
}
