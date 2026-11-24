import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { Plus } from "lucide-react";
import { Fragment, useState } from "react";
import { ApiError, api, data, type Schemas } from "@/api/client";
import { Ago } from "@/components/ago";
import { Card, CardInset, rows } from "@/components/card";
import { LoadError } from "@/components/load-error";
import { Notice } from "@/components/notice";
import { referenceTitles, SecretDialog } from "@/components/secret-dialog";
import { SettingsPage } from "@/components/settings/settings-page";
import { StatusDot } from "@/components/status-dot";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogPanel,
  DialogPopup,
  DialogTitle,
} from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { toastManager } from "@/components/ui/toast";
import { reference, sources } from "@/lib/secrets";
import { referenceTitle, secretsPage, sectionPage } from "@/lib/settings";
import { cn, textLink } from "@/lib/utils";
import { secretsQuery } from "@/queries/secrets";

type Secret = Schemas["Secret"];

export const Route = createFileRoute("/_app/settings/secrets")({
  loader: ({ context }) => void context.queryClient.prefetchQuery(secretsQuery),
  head: () => ({ meta: [{ title: "Secrets · otogi" }] }),
  component: SecretsPage,
});

// The columns of the list where the card is wide enough; in a narrow one each row stacks.
const columns =
  "@3xl:grid @3xl:grid-cols-[minmax(0,1.3fr)_7.5rem_minmax(0,1fr)_minmax(0,1.1fr)_10.5rem] @3xl:items-center @3xl:gap-x-4";

function SecretsPage() {
  const { data: secrets, error, refetch } = useQuery(secretsQuery);
  // setting is the secret given a value, or "new" for one being added.
  const [setting, setSetting] = useState<Secret | "new" | null>(null);
  const [removing, setRemoving] = useState<Secret | null>(null);
  return (
    <SettingsPage
      page={secretsPage}
      about={
        <>
          Tokens and keys that settings refer to by name, as{" "}
          <span className="font-mono text-[13px]">{reference("NAME")}</span>, so the settings never hold them. A value
          set here wins over the environment variable of that name; it is stored on the server, in the data directory,
          and never shown again.
        </>
      }
      action={
        <Button onClick={() => setSetting("new")}>
          <Plus /> Add secret
        </Button>
      }
    >
      <Card className="@container mt-6 max-w-4xl pt-3.5">
        {secrets && secrets.length > 0 && (
          <div aria-hidden className={cn("mx-3.5 hidden px-3 pb-2 font-medium text-muted-foreground text-xs", columns)}>
            <span>Name</span>
            <span>Value from</span>
            <span>Set</span>
            <span>Used by</span>
          </div>
        )}
        <CardInset>
          {error && !secrets && <LoadError what="the secrets" error={error} retry={() => void refetch()} />}
          {!secrets && !error && (
            <div className="flex flex-col gap-2 py-3">
              <Skeleton className="h-9 w-full" />
              <Skeleton className="h-9 w-full" />
            </div>
          )}
          {secrets?.length === 0 && (
            <p className="mx-auto max-w-[52ch] py-8 text-center text-muted-foreground text-sm">
              No secrets yet. Add one here, then choose it in settings wherever a token or key is asked for.
            </p>
          )}
          <ul className={rows}>
            {secrets?.map((s) => (
              <SecretRow key={s.name} secret={s} onSet={() => setSetting(s)} onRemove={() => setRemoving(s)} />
            ))}
          </ul>
        </CardInset>
      </Card>
      <SecretDialog
        secret={setting === "new" || setting === null ? undefined : setting}
        open={setting !== null}
        onOpenChange={(open) => !open && setSetting(null)}
      />
      <RemoveDialog secret={removing} close={() => setRemoving(null)} />
    </SettingsPage>
  );
}

function SecretRow({ secret: s, onSet, onRemove }: { secret: Secret; onSet: () => void; onRemove: () => void }) {
  const source = sources[s.source];
  return (
    <li className={cn("grid grid-cols-[minmax(0,1fr)_auto] gap-x-3 gap-y-1 py-3", columns)}>
      {/* A long name breaks after its underscores first. */}
      <span className="wrap-anywhere font-medium font-mono text-[13px]">
        {s.name.split(/(?<=_)/).map((part, i) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: the parts of one name, in order
          <Fragment key={i}>
            {part}
            <wbr />
          </Fragment>
        ))}
      </span>
      <span
        className={cn(
          "flex items-center gap-2 text-sm",
          s.source === "missing" && "font-medium text-destructive-foreground",
        )}
      >
        <StatusDot tone={source.tone} />
        <span className="sr-only">Value from: </span>
        {source.label}
      </span>
      <span className="col-span-full min-w-0 text-muted-foreground text-sm @3xl:col-auto">
        {s.source === "panel" ? (
          <>
            {s.by ? (
              <>
                <span className="@3xl:sr-only">Set by </span>
                <span className="wrap-anywhere">{s.by}</span>
              </>
            ) : (
              "Setter not recorded"
            )}
            {s.at && (
              <>
                {" · "}
                <Ago at={s.at} />
              </>
            )}
          </>
        ) : s.source === "environment" ? (
          "Outside the panel"
        ) : (
          "Nowhere: what uses it cannot be built"
        )}
      </span>
      <span className="col-span-full min-w-0 text-sm @3xl:col-auto">
        {s.references.length === 0 ? (
          <span className="text-muted-foreground">Not used</span>
        ) : (
          <>
            <span className="text-muted-foreground @3xl:sr-only">Used by </span>
            {s.references.map((id, i) => {
              // What refers to it is a module, or a section with a page here.
              const page = sectionPage(id);
              const link = page ? { to: page.to } : ({ to: "/modules/$module", params: { module: id } } as const);
              return (
                <Fragment key={id}>
                  {i > 0 && ", "}
                  <Link {...link} className={cn(textLink, "hit-target relative font-medium")}>
                    {referenceTitle(id)}
                  </Link>
                </Fragment>
              );
            })}
          </>
        )}
      </span>
      <span className="col-span-full mt-1.5 flex gap-2 @3xl:col-auto @3xl:mt-0 @3xl:justify-end">
        <Button variant="outline" size="sm" onClick={onSet} aria-label={`${actionLabel(s)} ${s.name}`}>
          {actionLabel(s)}
        </Button>
        {s.source === "panel" && (
          <Button
            variant="ghost"
            size="sm"
            className="text-muted-foreground"
            onClick={onRemove}
            aria-label={`Remove ${s.name}`}
          >
            Remove
          </Button>
        )}
      </span>
    </li>
  );
}

const actionLabel = (s: Secret) => (s.source === "panel" ? "Replace" : "Set value");

/** RemoveDialog confirms removing a secret set in the panel. Whether the
 * environment has the name is the server's to know: it refuses when not. */
function RemoveDialog({ secret, close }: { secret: Secret | null; close: () => void }) {
  return (
    <Dialog open={secret !== null} onOpenChange={(open) => !open && close()}>
      <DialogPopup className="sm:max-w-md">{secret && <RemoveConfirm secret={secret} close={close} />}</DialogPopup>
    </Dialog>
  );
}

function RemoveConfirm({ secret, close }: { secret: Secret; close: () => void }) {
  const queryClient = useQueryClient();
  const { name, references: used } = secret;
  const remove = useMutation({
    // The dialog shows why the bot refused.
    meta: { quiet: true },
    mutationFn: () => data(api.DELETE("/api/secrets/{name}", { params: { path: { name } } })),
    onSuccess: async ({ restarted }) => {
      await queryClient.invalidateQueries({ queryKey: ["secrets"] });
      void queryClient.invalidateQueries({ queryKey: ["modules"] });
      void queryClient.invalidateQueries({ queryKey: ["events"] });
      // The name stays in the list when settings still refer to it: the environment has it.
      const fallsBack = queryClient
        .getQueryData(secretsQuery.queryKey)
        ?.some((s) => s.name === name && s.source === "environment");
      toastManager.add({
        type: "success",
        title: `Removed ${name}`,
        description: fallsBack
          ? `Settings that refer to it now read the environment variable.${
              restarted.length > 0 ? ` ${referenceTitles(restarted)} restarted with it.` : ""
            }`
          : undefined,
      });
      close();
    },
  });
  // Settings refer to it and the environment does not have it: trying again changes nothing.
  const inUse = remove.error instanceof ApiError && remove.error.status === 409;
  return (
    <>
      <DialogHeader>
        <DialogTitle className="wrap-anywhere pe-8 leading-tight">
          Remove <span className="font-mono text-lg">{name}</span>?
        </DialogTitle>
        <DialogDescription>
          {used.length === 0
            ? "Nothing refers to it. Its value is deleted from the server, and cannot be brought back."
            : `The settings of ${referenceTitles(used)} refer to it. If the bot's environment has a variable of this name, they read that from now on${used.includes("system:accounts") ? ", the platform accounts from the bot's next start" : ""}; if not, the bot refuses and nothing changes.`}
        </DialogDescription>
      </DialogHeader>
      {remove.error && (
        <DialogPanel>
          <Notice role="alert" title="The bot refused, and nothing changed">
            <p className="mt-0.5">{remove.error.message}</p>
          </Notice>
        </DialogPanel>
      )}
      <DialogFooter>
        <DialogClose render={<Button variant="ghost" />}>{inUse ? "Close" : "Cancel"}</DialogClose>
        {!inUse && (
          <Button variant="destructive" loading={remove.isPending} onClick={() => remove.mutate()}>
            {remove.error ? "Try again" : "Remove"}
          </Button>
        )}
      </DialogFooter>
    </>
  );
}
