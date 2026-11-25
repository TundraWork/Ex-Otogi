import { createFileRoute } from "@tanstack/react-router";
import { ProviderModels } from "@/components/settings/llm-models";
import { settingsSearch } from "@/components/settings/settings-card";
import { SectionPage } from "@/components/settings/settings-page";
import { llmPage } from "@/lib/settings";
import { settingsQuery } from "@/queries/modules";
import { llmProvidersQuery } from "@/queries/system";

export const Route = createFileRoute("/_app/settings/llm")({
  validateSearch: settingsSearch,
  loader: ({ context }) => {
    void context.queryClient.prefetchQuery(settingsQuery({ section: llmPage.section }));
    void context.queryClient.prefetchQuery(llmProvidersQuery);
  },
  head: () => ({ meta: [{ title: "Language models · otogi" }] }),
  component: () => (
    <SectionPage
      page={llmPage}
      about="The providers modules reach language models through, each under the name modules use for it. Saving rebuilds the providers and restarts the running modules that use one. If one of those modules cannot be rebuilt, nothing is saved."
    >
      <ProviderModels />
    </SectionPage>
  ),
});
