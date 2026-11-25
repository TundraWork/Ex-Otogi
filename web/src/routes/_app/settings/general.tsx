import { createFileRoute } from "@tanstack/react-router";
import { Notice } from "@/components/notice";
import { settingsSearch } from "@/components/settings/settings-card";
import { SectionPage } from "@/components/settings/settings-page";
import { shorter } from "@/lib/duration";
import { generalPage } from "@/lib/settings";
import { settingsQuery } from "@/queries/modules";

export const Route = createFileRoute("/_app/settings/general")({
  validateSearch: settingsSearch,
  loader: ({ context }) => void context.queryClient.prefetchQuery(settingsQuery({ section: generalPage.section })),
  head: () => ({ meta: [{ title: "General · otogi" }] }),
  component: () => (
    <SectionPage
      page={generalPage}
      about="Who operates the bot, what starts a command, how long the event log is kept, and how much it logs. A saved change applies at once, and nothing restarts."
      review={(was, now, schema) => {
        // Unset, the retention is the default the schema names.
        const unset = schema.properties?.eventlog_retention?.default;
        const to = now.eventlog_retention ?? unset;
        return (
          shorter(was.eventlog_retention ?? unset, to) && (
            <Notice tone="warning" title="A shorter retention deletes records">
              <p className="mt-0.5">
                Event log records and chat history older than {String(to)} are deleted at the next cleanup, and cannot
                be brought back.
              </p>
            </Notice>
          )
        );
      }}
    />
  ),
});
