import { queryOptions } from "@tanstack/react-query";
import { api, data } from "@/api/client";

/** sectionsQuery lists the sections of the settings outside modules: for
 * each, its latest revision and the one the process runs with. */
export const sectionsQuery = queryOptions({
  queryKey: ["sections"],
  queryFn: async () => (await data(api.GET("/api/system"))) ?? [],
});

/** startupQuery is what the process read from its config file when it started. */
export const startupQuery = queryOptions({
  queryKey: ["startup"],
  queryFn: () => data(api.GET("/api/system/startup")),
});

/** restartKey is the key of the mutation that waits for the bot to come back. */
export const restartKey = ["restart"];

const second = () => new Promise((resolve) => setTimeout(resolve, 1000));

/** restart asks the bot to stop and be started again, and returns when the
 * process asked had started: what untilBack tells it from the next by. */
export async function restart(): Promise<string> {
  const { started_at } = await data(api.GET("/api/status"));
  await data(api.POST("/api/restart"));
  return started_at;
}

/** untilBack waits, for as long as it takes, until a process started after
 * the one that started at the time given answers. It does not wait for that
 * process to be ready: a platform that will not connect leaves the bot
 * unready with the panel reachable, and the Status page is where that shows. */
export async function untilBack(startedAt: string): Promise<void> {
  for (;;) {
    await second();
    try {
      const { response, data: status } = await api.GET("/api/status");
      // A bot that answers and asks for a sign-in is back too.
      if (response.status === 401 || (status && status.started_at !== startedAt)) return;
    } catch {
      // Nothing answers: the bot is still down.
    }
  }
}

/** llmProvidersQuery lists the configured language model providers, each
 * with the models last fetched from it. */
export const llmProvidersQuery = queryOptions({
  queryKey: ["llm-providers"],
  queryFn: async () => (await data(api.GET("/api/system/llm/providers"))) ?? [],
});

/** fetchModels asks the bot to fetch a provider's models now; it answers
 * with the provider and its new list, or fails with the provider's reason. */
export function fetchModels(name: string) {
  return data(api.POST("/api/system/llm/providers/{name}/models", { params: { path: { name } } }));
}
