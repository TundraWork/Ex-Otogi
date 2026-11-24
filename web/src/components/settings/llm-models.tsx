import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { format } from "date-fns";
import { ChevronRight, CircleAlert, ImageOff, RefreshCw } from "lucide-react";
import { Card, CardHeader, CardInset, rows } from "@/components/card";
import { LoadError } from "@/components/load-error";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsiblePanel, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Skeleton } from "@/components/ui/skeleton";
import { toastManager } from "@/components/ui/toast";
import { kinds, limits, type Model, type Provider, takesImages } from "@/lib/llm";
import { since } from "@/lib/time";
import { fetchModels, llmProvidersQuery } from "@/queries/system";

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;

/** ProviderModels lists each configured provider's models as last fetched,
 * and fetches them again when asked: the lists the model fields offer. */
export function ProviderModels() {
  const { data: providers, error, refetch } = useQuery(llmProvidersQuery);
  return (
    <Card>
      <CardHeader
        title="Models"
        description="What each provider offers, fetched only when you ask. The model fields in modules' settings offer these lists; a name not in them still saves."
      />
      <CardInset>
        {error ? (
          <LoadError what="the providers' models" error={error} retry={() => void refetch()} />
        ) : !providers ? (
          <Skeleton className="my-3 h-12 w-full rounded-lg" />
        ) : providers.length === 0 ? (
          <p className="py-6 text-center text-muted-foreground text-sm">
            No provider is set yet. Add one above and save, then fetch its models here.
          </p>
        ) : (
          <ul className={rows}>
            {providers.map((p) => (
              <ProviderRow key={p.name} provider={p} />
            ))}
          </ul>
        )}
      </CardInset>
    </Card>
  );
}

function ProviderRow({ provider: p }: { provider: Provider }) {
  const queryClient = useQueryClient();
  const fetch = useMutation({
    mutationFn: () => fetchModels(p.name),
    // The row says why a fetch failed, until the next one.
    meta: { quiet: true },
    onSuccess: (fetched) => {
      queryClient.setQueryData(llmProvidersQuery.queryKey, (all) =>
        all?.map((x) => (x.name === fetched.name ? fetched : x)),
      );
      toastManager.add({ type: "success", title: `${plural(fetched.models.length, "model")} fetched from ${p.name}` });
    },
    // The fetch is in the event log either way.
    onSettled: () => void queryClient.invalidateQueries({ queryKey: ["events"] }),
  });
  const fetchLabel = p.fetched_at ? "Fetch again" : "Fetch models";
  return (
    <li className="py-3">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <div className="min-w-0 flex-1 basis-56">
          <div className="flex flex-wrap items-baseline gap-x-2">
            <span className="font-medium font-mono text-sm">{p.name}</span>
            <span className="text-muted-foreground text-xs">{kinds[p.kind]}</span>
          </div>
          <p className="text-muted-foreground text-xs">
            {p.fetched_at ? (
              <>
                {plural(p.models.length, "model")}, fetched{" "}
                <time dateTime={p.fetched_at} title={format(new Date(p.fetched_at), "MMM d, yyyy HH:mm:ss")}>
                  {since(p.fetched_at)}
                </time>
                {p.fetched_by && ` by ${p.fetched_by}`}
              </>
            ) : (
              "No list fetched yet"
            )}
          </p>
        </div>
        <Button
          variant="outline"
          size="sm"
          loading={fetch.isPending}
          onClick={() => fetch.mutate()}
          aria-label={`${fetchLabel} (${p.name})`}
        >
          <RefreshCw /> {fetchLabel}
        </Button>
      </div>
      {fetch.isPending && (
        <p role="status" className="mt-1.5 text-muted-foreground text-xs">
          Asking {p.name} for its models…
        </p>
      )}
      {fetch.error && (
        <div role="alert" className="mt-2 flex gap-2 text-xs">
          <CircleAlert className="mt-px size-3.5 flex-none text-destructive-foreground" />
          <div className="min-w-0 text-muted-foreground">
            <p className="font-medium text-destructive-foreground">Could not fetch the models</p>
            <p className="wrap-anywhere">{fetch.error.message}</p>
            {p.fetched_at && <p>The list fetched before stays.</p>}
          </div>
        </div>
      )}
      {p.models.length > 0 && <ModelList provider={p} />}
    </li>
  );
}

function ModelList({ provider: p }: { provider: Provider }) {
  return (
    <Collapsible className="mt-1.5">
      <CollapsibleTrigger className="hit-target group relative -ms-1 flex items-center gap-1 rounded-md px-1 py-0.5 text-muted-foreground text-xs outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring">
        <ChevronRight className="size-3.5 transition-transform group-data-panel-open:rotate-90" />
        <span className="group-data-panel-open:hidden">Show the {plural(p.models.length, "model")}</span>
        <span className="hidden group-data-panel-open:inline">Hide the models</span>
      </CollapsibleTrigger>
      <CollapsiblePanel>
        <ul className="mt-1.5 flex flex-col divide-y divide-border/60 rounded-lg border bg-background">
          {p.models.map((m) => (
            <ModelRow key={m.id} model={m} />
          ))}
        </ul>
      </CollapsiblePanel>
    </Collapsible>
  );
}

function ModelRow({ model: m }: { model: Model }) {
  const sized = limits(m);
  const levels = m.thinking_levels?.length ? `thinking ${m.thinking_levels.join(", ")}` : "";
  return (
    <li className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 px-2.5 py-1.5">
      <span className="wrap-anywhere min-w-0 font-mono text-[13px]">{m.id}</span>
      {m.display_name && <span className="text-muted-foreground text-xs">{m.display_name}</span>}
      <span className="ms-auto flex flex-wrap items-center gap-x-2 text-muted-foreground text-xs tabular">
        {[sized, levels].filter(Boolean).join(" · ")}
        {takesImages(m) === false && (
          <span className="flex items-center gap-1 text-warning-foreground">
            <ImageOff className="size-3.5" aria-hidden /> No images
          </span>
        )}
      </span>
    </li>
  );
}
