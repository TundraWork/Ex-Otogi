import {
  useInfiniteQuery,
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { createFileRoute, Link, notFound } from "@tanstack/react-router";
import { useVirtualizer } from "@tanstack/react-virtual";
import { format, isToday, isYesterday } from "date-fns";
import { ArrowDown, ArrowLeft, Copy, ExternalLink, Pencil, ScrollText, UserCog, X } from "lucide-react";
import { type ReactElement, useEffect, useLayoutEffect, useRef, useState } from "react";
import { ApiError, api, data, type Schemas } from "@/api/client";
import { Ago } from "@/components/ago";
import { LoadError } from "@/components/load-error";
import { NotFound } from "@/components/route-states";
import { listed, type PeopleList, peopleLists } from "@/components/settings/people-lists";
import { type JSONSchema, label } from "@/components/settings/schema";
import type { SettingsSearch } from "@/components/settings/settings-card";
import { outcomeTone, StatusDot } from "@/components/status-dot";
import { Tile } from "@/components/tile";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBackdrop,
  DialogClose,
  DialogPortal,
  DialogPrimitive,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Menu, MenuLinkItem, MenuPopup, MenuTrigger } from "@/components/ui/menu";
import { Sheet, SheetHeader, SheetPanel, SheetPopup, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { toastManager } from "@/components/ui/toast";
import { chatKind, chatName } from "@/lib/chats";
import { listMeta, moduleMeta, moduleState, platformMeta } from "@/lib/meta";
import { sectionPages } from "@/lib/settings";
import { cn } from "@/lib/utils";
import { chatQuery, messagesQuery } from "@/queries/chats";
import { modulesQuery, type Subject, settingsQuery } from "@/queries/modules";

export const Route = createFileRoute("/_app/chats/$chat")({
  loader: async ({ context, params }) => {
    try {
      return await context.queryClient.ensureQueryData(chatQuery(params.chat));
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) throw notFound();
      throw e;
    }
  },
  head: ({ loaderData }) => ({ meta: [{ title: `${loaderData ? chatName(loaderData) : "Chat"} · otogi` }] }),
  component: ChatPage,
  notFoundComponent: NoSuchChat,
});

function NoSuchChat() {
  const { chat } = Route.useParams();
  return (
    <NotFound
      title="No such chat"
      detail={
        <>
          The bot has not seen a chat with the ID <span className="font-mono text-xs">{chat}</span>.
        </>
      }
    >
      <Button variant="outline" render={<Link to="/chats" />}>
        All chats
      </Button>
    </NotFound>
  );
}

function ChatPage() {
  const { chat: id } = Route.useParams();
  const { data: chat } = useSuspenseQuery(chatQuery(id));
  const p = platformMeta(chat.platform);
  return (
    <div className="grid min-h-0 flex-1 grid-cols-1 grid-rows-[minmax(0,1fr)] lg:grid-cols-[minmax(0,1fr)_19rem]">
      <section className="flex min-h-0 flex-col">
        <header className="flex items-center gap-3 border-b px-4 py-3.5 sm:px-6">
          <Link
            to="/chats"
            activeOptions={{ exact: true }}
            className="hit-target relative -ml-1 rounded-md p-1 text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring md:hidden"
            aria-label="All chats"
          >
            <ArrowLeft className="size-5" />
          </Link>
          <Tile tone={p.tone} icon={p.icon} />
          <div className="min-w-0 flex-1">
            <h1 className="truncate font-semibold text-[17px] leading-tight tracking-[-0.01em]" title={chatName(chat)}>
              {chatName(chat)}
            </h1>
            <span className="block truncate font-mono text-muted-foreground text-xs" title={chat.id}>
              {chat.id}
            </span>
          </div>
          <Button
            variant="outline"
            size="sm"
            render={<Link to="/events" search={{ conversation: chat.conversation }} />}
          >
            <ScrollText /> <span className="max-sm:sr-only">Open in event log</span>
          </Button>
          <Sheet>
            <SheetTrigger render={<Button variant="outline" size="sm" className="lg:hidden" />}>About</SheetTrigger>
            <SheetPopup>
              <SheetHeader>
                <SheetTitle>{chatName(chat)}</SheetTitle>
              </SheetHeader>
              <SheetPanel>
                <About chat={chat} />
              </SheetPanel>
            </SheetPopup>
          </Sheet>
        </header>
        <div className="border-b px-4 py-2 lg:hidden">
          <ModuleSwitches chat={chat} compact />
        </div>
        <Conversation chat={id} people={senders(chat)} />
      </section>
      <aside className="hidden min-h-0 flex-col gap-5 overflow-y-auto border-l px-4 py-5 lg:flex">
        <ModuleSwitches chat={chat} />
        <About chat={chat} />
      </aside>
    </div>
  );
}

type Message = Schemas["ChatMessage"];

/** Conversation shows a chat's messages, oldest at the top, and loads
 * older ones as one scrolls up. */
/** senders names people by what the directory knows: their latest name,
 * and in a private chat, the chat's name for the other person. */
function senders(chat: Schemas["ChatOutputBody"]): Record<string, string> {
  const names: Record<string, string> = {};
  if (chat.kind === "direct") names[chat.conversation] = chatName(chat);
  for (const p of chat.people_seen) if (p.name) names[p.actor] = p.name;
  return names;
}

function Conversation({ chat, people }: { chat: string; people: Record<string, string> }) {
  const messages = useInfiniteQuery(messagesQuery(chat));
  const { data: modules } = useQuery(modulesQuery);
  const items = (messages.data?.pages.flatMap((p) => p.messages ?? []) ?? []).toReversed();
  const scroller = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => scroller.current,
    estimateSize: () => 76,
    overscan: 8,
    getItemKey: (i) => items[i]?.id ?? i,
  });
  // Keep the reader's place: start at the newest and stay there while the
  // reader is there, as rows are measured, pictures load, and messages
  // arrive; when older messages arrive above, keep the ones in view where
  // they were.
  const newest = items.at(-1)?.id;
  const oldest = items[0]?.id;
  const placed = useRef<{ oldest?: number; height: number; atNewest: boolean }>({ height: 0, atNewest: true });
  // away is the newest message when the reader scrolled up from it, so the
  // jump button can say when newer ones arrived.
  const [away, setAway] = useState<number>();
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el || items.length === 0) return;
    const before = placed.current;
    const height = virtualizer.getTotalSize();
    if (before.atNewest) {
      el.scrollTop = el.scrollHeight;
    } else if (oldest !== before.oldest) {
      el.scrollTop += height - before.height;
    }
    placed.current = { ...before, oldest, height };
  });
  // Older messages load when the top is near: after scrolling there, and
  // after any render that leaves it there, as when the first page does not
  // fill a tall window and there is nothing to scroll.
  const loadOlder = () => {
    const el = scroller.current;
    if (!el || !messages.hasNextPage || messages.isFetchingNextPage || messages.error) return;
    if (el.scrollTop < 240) void messages.fetchNextPage();
  };
  useEffect(loadOlder);
  const labels = Object.fromEntries(
    (modules ?? []).flatMap((m) => m.allowlists.map((l, i) => [`${m.id}/${l.name}`, listMeta(m.id, l, i)])),
  );
  if (messages.isPending) {
    return (
      <div className="flex flex-1 flex-col gap-4 p-6">
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-14 w-2/3 rounded-xl" />
        ))}
      </div>
    );
  }
  if (messages.error && items.length === 0) {
    return (
      <LoadError
        className="mx-auto w-full max-w-lg px-4 py-12"
        what="this chat's messages"
        error={messages.error}
        retry={() => void messages.refetch()}
      />
    );
  }
  const toNewest = () => {
    placed.current.atNewest = true;
    setAway(undefined);
  };
  return (
    <div className="relative flex min-h-0 flex-1 flex-col">
      <div
        ref={scroller}
        className="min-h-0 flex-1 overflow-y-auto px-4 sm:px-6"
        onScroll={(e) => {
          const el = e.currentTarget;
          loadOlder();
          const gap = el.scrollHeight - el.scrollTop - el.clientHeight;
          placed.current.atNewest = gap < 8;
          const far = gap > 320;
          if (far && away === undefined) setAway(newest);
          if (!far && away !== undefined) setAway(undefined);
        }}
      >
        {items.length === 0 && (
          <p className="py-16 text-center text-muted-foreground text-sm">
            Nothing said here within the event log's retention.
          </p>
        )}
        {messages.error && (
          <LoadError
            className="mx-auto max-w-lg"
            what="older messages"
            error={messages.error}
            retry={() => void (messages.isFetchNextPageError ? messages.fetchNextPage() : messages.refetch())}
          />
        )}
        <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
          {virtualizer.getVirtualItems().map((row) => {
            const m = items[row.index] as Message;
            const prev = items[row.index - 1];
            const newDay = !prev || new Date(prev.at).toDateString() !== new Date(m.at).toDateString();
            return (
              <div
                key={row.key}
                data-index={row.index}
                ref={virtualizer.measureElement}
                className="absolute inset-x-0 top-0 pt-3.5"
                style={{ transform: `translateY(${row.start}px)` }}
              >
                {newDay && <DayLabel at={m.at} />}
                <MessageRow chat={chat} m={m} labels={labels} people={people} />
              </div>
            );
          })}
        </div>
        <div className="h-5" />
      </div>
      {away !== undefined && (
        <Button
          variant="outline"
          size={newest !== away ? "sm" : "icon"}
          onClick={toNewest}
          aria-label="Jump to the newest message"
          className="absolute right-4 bottom-4 rounded-full bg-background shadow-md/10 sm:right-6"
        >
          {newest !== away && <span className="ps-1 font-medium text-xs">New</span>}
          <ArrowDown />
        </Button>
      )}
    </div>
  );
}

function DayLabel({ at }: { at: string }) {
  const d = new Date(at);
  const text = isToday(d) ? "Today" : isYesterday(d) ? "Yesterday" : format(d, "EEEE, MMMM d");
  return <div className="mb-3.5 text-center text-muted-foreground text-xs">{text}</div>;
}

type Reply = { profile?: string; model?: string };

function MessageRow({
  chat,
  m,
  labels,
  people,
}: {
  chat: string;
  m: Message;
  labels: Record<string, ReturnType<typeof listMeta>>;
  people: Record<string, string>;
}) {
  const records = m.records ?? [];
  const own = records.find((r) => !r.kind.startsWith("module."));
  const done = records.find((r) => r.kind.startsWith("module."));
  const reply = own?.payload as Reply | undefined;
  const meta = m.bot
    ? (reply?.profile && labels[`${m.module}/${reply.profile}`]) || moduleMeta(m.module ?? "")
    : undefined;
  const actor = m.actor?.id ?? "";
  const who = m.bot ? (meta?.title ?? "Bot") : m.actor?.name || people[actor] || actor.split(":").pop() || "Someone";
  const parts = m.message.parts ?? [];
  const text = parts
    .filter((part) => part.kind === "text" && part.text)
    .map((part) => part.text)
    .join("\n");
  const media = parts.filter((part) => part.media);
  const outcome = own?.outcome ?? done?.outcome;
  return (
    <div className="flex gap-2.5">
      {meta ? (
        <Tile tone={meta.tone} icon={meta.icon} size="sm" className="mt-0.5 rounded-full" />
      ) : (
        <span className="mt-0.5 grid size-7 flex-none place-items-center rounded-full bg-linear-to-b from-neutral-100 to-neutral-200 font-semibold text-neutral-600 text-xs shadow-[inset_0_0_0_1px_--theme(--color-black/5%)]">
          {who.slice(0, 1).toUpperCase()}
        </span>
      )}
      <div className="min-w-0 max-w-[36rem]">
        <div className="text-[12.5px] text-muted-foreground">
          <span className="mr-1.5 font-semibold text-foreground">{who}</span>
          <time dateTime={m.at}>{format(new Date(m.at), "HH:mm")}</time>
        </div>
        {text && (
          <div
            className={cn(
              "mt-1 w-fit whitespace-pre-wrap wrap-anywhere rounded-[4px_14px_14px_14px] px-3 py-2 text-sm leading-relaxed",
              m.bot ? "border bg-background shadow-xs/5" : "bg-neutral-100",
            )}
          >
            {text}
          </div>
        )}
        {media.map((part) => (
          <MediaPart key={part.id} chat={chat} message={m.id} part={part} />
        ))}
        {m.bot && (
          <div className="mt-1.5 flex items-center gap-2 font-mono text-muted-foreground text-xs">
            <StatusDot tone={outcomeTone(outcome)} label={outcome === "ok" ? "ok" : undefined} />
            {[
              outcome !== "ok" && (outcome ?? "no outcome"),
              meta?.title,
              own?.kind,
              done?.duration_ms !== undefined && `${(done.duration_ms / 1000).toFixed(1)} s`,
              reply?.model,
            ]
              .filter(Boolean)
              .join(" · ")}
          </div>
        )}
      </div>
    </div>
  );
}

function MediaPart({ chat, message, part }: { chat: string; message: number; part: Schemas["Part"] }) {
  const [failed, setFailed] = useState(false);
  const src = `/api/chats/${encodeURIComponent(chat)}/media/${message}/${encodeURIComponent(part.id)}`;
  // An <img> does not say why it failed; asking again does.
  const reason = useQuery({
    queryKey: ["media-failure", src],
    enabled: failed,
    staleTime: Number.POSITIVE_INFINITY,
    queryFn: async () => {
      const response = await fetch(src);
      return response.ok ? "" : new ApiError(response.status, await response.json().catch(() => undefined)).message;
    },
  });
  const image =
    part.media?.role === "image" || part.media?.role === "sticker" || part.media?.media_type?.startsWith("image/");
  if (!image || failed) {
    return (
      <div className="mt-1 w-fit rounded-xl border bg-background px-3 py-2 text-muted-foreground text-xs">
        {failed
          ? `This image could not be loaded${reason.data ? `: ${reason.data}` : "."}`
          : (part.media?.filename ?? part.media?.role ?? "File")}
      </div>
    );
  }
  return (
    <Dialog>
      <DialogTrigger
        className="mt-1 block w-fit cursor-zoom-in rounded-xl outline-none focus-visible:ring-2 focus-visible:ring-ring"
        aria-label="Show the picture"
      >
        <img
          src={src}
          alt=""
          loading="lazy"
          onError={() => setFailed(true)}
          className="h-auto max-h-80 w-auto max-w-[min(20rem,100%)] rounded-xl border shadow-xs/5"
          width={part.media?.width}
          height={part.media?.height}
        />
      </DialogTrigger>
      <Picture src={src} />
    </Dialog>
  );
}

/** Picture shows an image over the page, as large as the window allows;
 * Escape, the close button, or a click beside it closes it. */
function Picture({ src }: { src: string }) {
  return (
    <DialogPortal>
      <DialogBackdrop className="bg-black/80 backdrop-blur-none" />
      <DialogPrimitive.Viewport className="fixed inset-0 z-50 flex items-center justify-center p-4 sm:p-10">
        <DialogPrimitive.Popup className="relative flex max-h-full max-w-full flex-col items-center gap-3 outline-none transition-[scale,opacity] duration-200 data-ending-style:scale-98 data-ending-style:opacity-0 data-starting-style:scale-98 data-starting-style:opacity-0">
          <DialogPrimitive.Title className="sr-only">Picture</DialogPrimitive.Title>
          <img src={src} alt="" className="min-h-0 max-w-full flex-1 rounded-lg object-contain shadow-2xl" />
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              render={<a href={src} target="_blank" rel="noreferrer" />}
              className="bg-background/90"
            >
              <ExternalLink /> Open original
            </Button>
            <DialogClose render={<Button variant="outline" size="sm" className="bg-background/90" />}>
              <X /> Close
            </DialogClose>
          </div>
        </DialogPrimitive.Popup>
      </DialogPrimitive.Viewport>
    </DialogPortal>
  );
}

function ModuleSwitches({ chat, compact }: { chat: Schemas["ChatOutputBody"]; compact?: boolean }) {
  const { data: modules, error, refetch } = useQuery(modulesQuery);
  const queryClient = useQueryClient();
  const toggle = useMutation({
    mutationFn: ({ module, list, on }: { module: string; list: string; title: string; on: boolean }) => {
      const params = { params: { path: { module, list, chat: chat.id } } };
      return data(
        on
          ? api.PUT("/api/modules/{module}/allowlists/{list}/chats/{chat}", params)
          : api.DELETE("/api/modules/{module}/allowlists/{list}/chats/{chat}", params),
      );
    },
    onSuccess: (_, { title, on }) =>
      toastManager.add({ type: "success", title: `${title} is ${on ? "on" : "off"} in ${chatName(chat)}` }),
    onSettled: async () => {
      await queryClient.invalidateQueries({ queryKey: ["chats"] });
      await queryClient.invalidateQueries({ queryKey: ["modules"] });
    },
  });
  const lists = (modules ?? []).flatMap((m) => m.allowlists.map((l, i) => ({ m, l, meta: listMeta(m.id, l, i) })));
  const on = (module: string, list: string) => chat.lists.some((x) => x.module === module && x.list === list);
  if (error && !modules) {
    return <LoadError what="the modules" error={error} retry={() => void refetch()} />;
  }
  if (compact) {
    return (
      <ul className="-mx-1 flex gap-2 overflow-x-auto px-1 py-0.5">
        {lists.map(({ m, l, meta }) => (
          <li
            key={`${m.id}/${l.name}`}
            className="flex flex-none items-center gap-2 rounded-full border bg-background py-1 ps-1 pe-1.5 shadow-xs/5"
          >
            <Tile tone={meta.tone} icon={meta.icon} size="sm" className="size-6 rounded-full" />
            <span className="font-medium text-[13px]">{meta.title}</span>
            <Switch
              checked={on(m.id, l.name)}
              disabled={toggle.isPending}
              onCheckedChange={(v) => toggle.mutate({ module: m.id, list: l.name, title: meta.title, on: v })}
              aria-label={meta.toggle}
            />
          </li>
        ))}
      </ul>
    );
  }
  return (
    <section>
      <h2 className="mb-2 px-0.5 font-semibold text-[13px]">Modules in this chat</h2>
      <div className="surface-raised rounded-2xl px-3">
        <ul className="divide-y divide-border/80">
          {lists.map(({ m, l, meta }) => {
            const checked = on(m.id, l.name);
            const pending = toggle.isPending && toggle.variables.module === m.id && toggle.variables.list === l.name;
            return (
              <li key={`${m.id}/${l.name}`} className="flex items-center gap-2.5 py-2.5">
                <Tile
                  tone={meta.tone}
                  icon={meta.icon}
                  size="sm"
                  className={cn(!m.running && "opacity-55 saturate-50")}
                />
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-medium text-sm">{meta.title}</span>
                  {!m.running && (
                    <span
                      className={cn(
                        "block text-xs",
                        m.enabled ? "text-destructive-foreground" : "text-muted-foreground",
                      )}
                    >
                      Module: {moduleState(m).label}
                    </span>
                  )}
                </span>
                <Switch
                  checked={pending ? toggle.variables.on : checked}
                  disabled={pending}
                  onCheckedChange={(v) => toggle.mutate({ module: m.id, list: l.name, title: meta.title, on: v })}
                  aria-label={meta.toggle}
                />
              </li>
            );
          })}
          {lists.length === 0 && (
            <li className="py-4 text-muted-foreground text-sm">No module answers in chosen chats.</li>
          )}
        </ul>
      </div>
    </section>
  );
}

function About({ chat }: { chat: Schemas["ChatOutputBody"] }) {
  const days = chat.days ?? [];
  const week = Array.from({ length: 7 }, (_, i) => {
    const d = new Date(Date.now() - (6 - i) * 86_400_000).toISOString().slice(0, 10);
    return { day: d, messages: days.find((x) => x.day === d)?.messages ?? 0 };
  });
  const peak = Math.max(1, ...week.map((d) => d.messages));
  const facts = [
    { label: "Platform", value: chatKind(chat) },
    { label: "Bot account", value: chat.account },
    ...(chat.members ? [{ label: "Members", value: chat.members }] : []),
    { label: "People seen", value: chat.people },
    { label: "First seen", value: format(new Date(chat.first_seen), "MMM d, HH:mm") },
    { label: "Messages, 7 days", value: week.reduce((n, d) => n + d.messages, 0) },
  ];
  return (
    <section>
      <h2 className="mb-2 px-0.5 font-semibold text-[13px]">About this chat</h2>
      <div className="surface-raised rounded-2xl px-3">
        <Name chat={chat} />
        {chat.description && (
          <p className="whitespace-pre-wrap border-t border-border/80 py-2.5 text-[13px] text-muted-foreground">
            {chat.description}
          </p>
        )}
        <dl className="divide-y divide-border/80 border-t border-border/80">
          {facts.map((f) => (
            <div key={f.label} className="flex justify-between gap-3 py-2 text-[13px]">
              <dt className="text-muted-foreground">{f.label}</dt>
              <dd className="truncate font-medium tabular">{f.value}</dd>
            </div>
          ))}
        </dl>
        <div className="border-t border-border/80 py-3">
          <div className="flex h-9 items-end gap-1.5" role="img" aria-label="New messages per day over the last 7 days">
            {week.map((d, i) => (
              <div key={d.day} className="flex flex-1 flex-col items-center gap-1">
                <div
                  className={cn("w-full rounded-[3px]", i === week.length - 1 ? "bg-neutral-800" : "bg-neutral-300")}
                  style={{ height: `${Math.max(2, (d.messages / peak) * 32)}px` }}
                  title={`${d.day}: ${d.messages}`}
                />
              </div>
            ))}
          </div>
          <div className="mt-1 flex justify-between text-muted-foreground text-xs">
            <span>{format(new Date(week[0]?.day ?? Date.now()), "MMM d")}</span>
            <span>Today</span>
          </div>
        </div>
      </div>
      <People platform={chat.platform} people={chat.people_seen} />
    </section>
  );
}

/** Place is a list naming people in a module's or a section's settings, and
 * a link to its page that adds a person to it. */
type Place = { title: string; list: PeopleList; settings: unknown; link: (person: string) => ReactElement };

/** usePlaces are the lists naming people in the settings of every module and
 * section, as their schemas have them (see people-lists). */
function usePlaces(): Place[] {
  const { data: modules = [] } = useQuery(modulesQuery);
  const subjects = [
    ...modules.map((m) => ({
      of: { module: m.id } as Subject,
      title: moduleMeta(m.id).title,
      link: (search: SettingsSearch) => <Link to="/modules/$module" params={{ module: m.id }} search={search} />,
    })),
    ...sectionPages.map((p) => ({
      of: { section: p.section } as Subject,
      title: p.title,
      link: (search: SettingsSearch) => <Link to={p.to} search={search} />,
    })),
  ];
  const found = useQueries({ queries: subjects.map((s) => settingsQuery(s.of)) });
  return subjects.flatMap((s, i) => {
    const current = found[i]?.data;
    if (!current) return [];
    return peopleLists(current.schema as JSONSchema).map((list) => ({
      title: `${s.title} · ${label(list.key)}`,
      list,
      settings: current.settings,
      link: (person: string) => s.link({ person, list: list.key }),
    }));
  });
}

/** People lists who has been seen sending messages here, recent first, and
 * the lists naming people in the settings each is on, with the name the
 * list's item gives them. Each can be added to such a list, or found in it,
 * on its settings page. */
function People({ platform, people }: { platform: string; people: Schemas["Person"][] }) {
  const places = usePlaces();
  if (people.length === 0) return null;
  return (
    <>
      <h2 className="mt-5 mb-2 px-0.5 font-semibold text-[13px]">People</h2>
      <ul className="surface-raised divide-y divide-border/80 rounded-2xl px-3">
        {people.map((p) => {
          // The ID as settings and /whoami write it.
          const id = `${platform}:${p.actor}`;
          const on = places.map((place) => ({ place, at: listed(place.list, place.settings, id) }));
          const listedOn = on.filter((o) => o.at);
          return (
            <li key={p.actor} className="flex items-center gap-2.5 py-2">
              <span className="grid size-7 flex-none place-items-center rounded-full bg-linear-to-b from-neutral-100 to-neutral-200 font-semibold text-neutral-600 text-xs shadow-[inset_0_0_0_1px_--theme(--color-black/5%)]">
                {(p.name || "?").slice(0, 1).toUpperCase()}
              </span>
              <span className="min-w-0 flex-1">
                <span className="block truncate font-medium text-[13px]">{p.name || "Unnamed"}</span>
                <span className="block truncate font-mono text-muted-foreground text-xs" title={id}>
                  {id}
                </span>
                {listedOn.length > 0 && (
                  <span className="block text-muted-foreground text-xs">
                    {listedOn.map((o) => (o.at?.name ? `${o.place.title}: ${o.at.name}` : o.place.title)).join(", ")}
                  </span>
                )}
              </span>
              <span className="text-right text-muted-foreground text-xs tabular">
                {p.messages}
                <Ago at={p.last_seen} className="block" />
              </span>
              {on.length > 0 && (
                <Menu>
                  <MenuTrigger
                    render={
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        className="text-muted-foreground"
                        aria-label={`Set up ${p.name || id}`}
                      />
                    }
                  >
                    <UserCog />
                  </MenuTrigger>
                  <MenuPopup align="end">
                    {on.map(({ place, at }) => (
                      <MenuLinkItem key={place.title} render={place.link(id)}>
                        {at ? `Edit in ${place.title}` : `Add to ${place.title}`}
                      </MenuLinkItem>
                    ))}
                  </MenuPopup>
                </Menu>
              )}
              <Button
                variant="ghost"
                size="icon-sm"
                className="-mr-1.5 text-muted-foreground"
                aria-label={`Copy the ID of ${p.name || id}`}
                onClick={() =>
                  void navigator.clipboard
                    .writeText(id)
                    .then(() => toastManager.add({ type: "success", title: `Copied ${id}` }))
                }
              >
                <Copy />
              </Button>
            </li>
          );
        })}
      </ul>
    </>
  );
}

function Name({ chat }: { chat: Schemas["ChatOutputBody"] }) {
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(chat.name ?? "");
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: () => data(api.PATCH("/api/chats/{chat}", { params: { path: { chat: chat.id } }, body: { name } })),
    onSuccess: async () => {
      setEditing(false);
      await queryClient.invalidateQueries({ queryKey: ["chats"] });
    },
  });
  if (!editing) {
    return (
      <div className="flex items-center gap-2 py-2.5">
        <div className="min-w-0 flex-1 text-[13px]">
          <div className="text-muted-foreground">Name</div>
          <div className="wrap-anywhere font-medium">
            {chat.name || (
              <span className="text-muted-foreground">{chat.title ? `${chat.title} (from the platform)` : "None"}</span>
            )}
          </div>
        </div>
        <Button variant="ghost" size="icon-sm" aria-label="Rename" onClick={() => setEditing(true)}>
          <Pencil />
        </Button>
      </div>
    );
  }
  return (
    <form
      className="flex items-center gap-2 py-2.5"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <Input
        size="sm"
        value={name}
        onChange={(e) => setName(e.target.value)}
        placeholder={chat.title || "Name"}
        autoFocus
      />
      <Button size="sm" type="submit" loading={save.isPending}>
        Save
      </Button>
    </form>
  );
}
