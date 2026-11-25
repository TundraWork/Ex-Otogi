import { useMutation, useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link, notFound } from "@tanstack/react-router";
import { format } from "date-fns";
import { ArrowLeft, Bot } from "lucide-react";
import { Suspense } from "react";
import { api, data, type Schemas } from "@/api/client";
import { AllowlistCard } from "@/components/allowlist-card";
import { Card, CardHeader, CardInset, rows } from "@/components/card";
import { LoadError } from "@/components/load-error";
import { Notice } from "@/components/notice";
import { NotFound } from "@/components/route-states";
import { addingOf, SettingsCard, settingsSearch } from "@/components/settings/settings-card";
import { outcomeTone, StatusDot } from "@/components/status-dot";
import { Tile } from "@/components/tile";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { toastManager } from "@/components/ui/toast";
import { listMeta, moduleMeta, moduleState } from "@/lib/meta";
import { cn } from "@/lib/utils";
import { modulesQuery, recordsQuery, settingsQuery } from "@/queries/modules";
import { useChatNames } from "@/queries/names";

export const Route = createFileRoute("/_app/modules/$module")({
  loader: async ({ context, params }) => {
    const modules = await context.queryClient.ensureQueryData(modulesQuery);
    if (!modules.some((m) => m.id === params.module)) throw notFound();
    void context.queryClient.prefetchQuery(settingsQuery({ module: params.module }));
  },
  validateSearch: settingsSearch,
  head: ({ params }) => ({ meta: [{ title: `${moduleMeta(params.module).title} · otogi` }] }),
  component: ModulePage,
  notFoundComponent: NoSuchModule,
});

function NoSuchModule() {
  const { module } = Route.useParams();
  return (
    <NotFound title={`No module called ${module}`} detail="The bot is built with the modules in the list.">
      <Button variant="outline" render={<Link to="/modules" />}>
        All modules
      </Button>
    </NotFound>
  );
}

function ModulePage() {
  const { module: id } = Route.useParams();
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const { data: modules } = useSuspenseQuery(modulesQuery);
  const m = modules.find((x) => x.id === id) as Schemas["Module"];
  const meta = moduleMeta(id);
  return (
    <div className="min-w-0 px-5 py-6 sm:px-10 sm:py-7">
      <Link
        to="/modules"
        activeOptions={{ exact: true }}
        className="hit-target relative mb-4 inline-flex items-center gap-1 rounded-md text-muted-foreground text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring md:hidden"
      >
        <ArrowLeft className="size-4" /> Modules
      </Link>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-3">
        <Tile tone={meta.tone} icon={meta.icon} size="lg" />
        <div className="min-w-0 flex-1 basis-56">
          <h1 className="font-bold text-2xl leading-tight tracking-[-0.035em] sm:text-[1.875rem]">{meta.title}</h1>
          <p className="text-muted-foreground text-sm">{meta.about}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {m.handles && <IgnoreBots module={m} />}
          <Power module={m} />
        </div>
      </div>
      {m.problem && (
        <Notice className="mt-6" title="Its settings do not build">
          <p className="mt-0.5">{m.problem}</p>
        </Notice>
      )}
      <div className="mt-6 grid grid-cols-1 items-start gap-4 xl:grid-cols-[minmax(0,1.15fr)_minmax(0,1fr)]">
        <Suspense fallback={<Skeleton className="h-96 w-full rounded-2xl" />}>
          <SettingsCard
            of={{ module: id }}
            adding={addingOf(search)}
            onAdded={() => void navigate({ search: {}, replace: true, resetScroll: false })}
          />
        </Suspense>
        <div className="flex flex-col gap-4">
          {m.allowlists.map((l, i) => (
            <AllowlistCard
              key={l.name}
              module={id}
              list={l.name}
              meta={listMeta(id, l, i)}
              description={l.description}
            />
          ))}
          <Activity module={m} />
        </div>
      </div>
    </div>
  );
}

function Power({ module: m }: { module: Schemas["Module"] }) {
  const queryClient = useQueryClient();
  const toggle = useMutation({
    mutationFn: (enabled: boolean) =>
      data(api.PATCH("/api/modules/{module}", { params: { path: { module: m.id } }, body: { enabled } })),
    onSuccess: (now) => {
      const state = moduleState(now);
      toastManager.add({
        type: state.tone === "error" ? "error" : "success",
        title: `${moduleMeta(m.id).title}: ${state.label}`,
        description: now.problem,
      });
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: ["modules"] }),
  });
  const on = toggle.isPending ? Boolean(toggle.variables) : m.enabled;
  const state = moduleState(m);
  return (
    <div className="flex items-center gap-3 rounded-full border bg-background py-1.5 ps-3 pe-1.5 shadow-xs/5">
      <StatusDot tone={state.tone} />
      <span className={cn("font-medium text-sm", state.tone === "error" && "text-destructive-foreground")}>
        {state.label}
      </span>
      <Switch
        checked={on}
        disabled={toggle.isPending}
        onCheckedChange={(v) => toggle.mutate(v)}
        aria-label="Run this module"
      />
    </div>
  );
}

/** IgnoreBots is the module's "ignore bots" switch: on, messages from bot
 * accounts never reach it. It applies at once. */
function IgnoreBots({ module: m }: { module: Schemas["Module"] }) {
  const queryClient = useQueryClient();
  const toggle = useMutation({
    mutationFn: (ignore_bots: boolean) =>
      data(api.PATCH("/api/modules/{module}", { params: { path: { module: m.id } }, body: { ignore_bots } })),
    onSuccess: (now) =>
      toastManager.add({
        type: "success",
        title: `${moduleMeta(m.id).title} ${now.ignore_bots ? "ignores" : "hears"} bot accounts`,
      }),
    onSettled: () => queryClient.invalidateQueries({ queryKey: ["modules"] }),
  });
  const on = toggle.isPending ? Boolean(toggle.variables) : m.ignore_bots;
  return (
    <div
      className="flex items-center gap-2.5 rounded-full border bg-background py-1.5 ps-3 pe-1.5 shadow-xs/5"
      title="Messages from bot accounts never reach this module. Accounts a program drives for a person (userbots) are not told apart."
    >
      <Bot className="size-4 text-muted-foreground" />
      <span className="font-medium text-sm">Ignore bots</span>
      <Switch
        checked={on}
        disabled={toggle.isPending}
        onCheckedChange={(v) => toggle.mutate(v)}
        aria-label="Ignore messages from bot accounts"
      />
    </div>
  );
}

function Activity({ module: m }: { module: Schemas["Module"] }) {
  const { data: records, error, refetch } = useQuery(recordsQuery(m.id));
  const chatName = useChatNames();
  const stats = [
    { label: "Completed", value: m.counts.completed },
    { label: "Slow", value: m.counts.slow },
    { label: "Failed", value: m.counts.failed + m.counts.panicked },
  ];
  return (
    <Card>
      <CardHeader title="Activity" note="since the bot started" />
      <div className="mx-3.5 grid grid-cols-3 gap-2">
        {stats.map((s) => (
          <div key={s.label} className="surface-inset rounded-xl px-3 py-2">
            <div className="text-muted-foreground text-xs">{s.label}</div>
            <div className="font-semibold text-lg tabular tracking-tight">{s.value}</div>
          </div>
        ))}
      </div>
      <CardInset className="mt-2">
        {error && !records && <LoadError what="its latest records" error={error} retry={() => void refetch()} />}
        <ul className={rows}>
          {(records?.records ?? []).map((r) => (
            <li key={r.id} className="flex items-center gap-2.5 py-2">
              <StatusDot tone={outcomeTone(r.outcome)} label={r.outcome ?? "no outcome"} />
              <span className="min-w-0 flex-1 truncate font-mono text-xs">
                {r.kind}
                {r.conversation && (
                  <span className="font-sans text-muted-foreground"> · {chatName(r.platform, r.conversation)}</span>
                )}
              </span>
              <time className="font-mono text-muted-foreground text-xs" dateTime={r.at}>
                {format(new Date(r.at), "MMM d HH:mm:ss")}
              </time>
            </li>
          ))}
          {records?.records?.length === 0 && (
            <li className="py-4 text-center text-muted-foreground text-sm">No records yet.</li>
          )}
        </ul>
      </CardInset>
    </Card>
  );
}
