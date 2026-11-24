import { keepPreviousData, queryOptions, useQueries } from "@tanstack/react-query";
import { api, data } from "@/api/client";
import type { Person } from "@/lib/people";

const people = async (q: string, limit: number): Promise<Person[]> =>
  (await data(api.GET("/api/people", { params: { query: { q, limit } } }))) ?? [];

/** peopleQuery finds the people the bot has seen by name or ID; nothing
 * typed is the most recently seen. While the next search runs, the last
 * one's people stay. */
export const peopleQuery = (q: string) =>
  queryOptions({
    queryKey: ["people", q.trim()],
    queryFn: () => people(q.trim(), 20),
    placeholderData: keepPreviousData,
    staleTime: 30_000,
  });

/** personQuery finds the person of an ID, which the search puts first;
 * undefined when the bot has not seen them. */
export const personQuery = (id: string) =>
  queryOptions({
    queryKey: ["people", "id", id],
    queryFn: async () => (await people(id, 1)).find((p) => p.id === id) ?? null,
    enabled: id.includes(":"),
    staleTime: 60_000,
  });

/** usePeopleNames is the names of the people of some IDs, by ID, for those
 * the bot has seen with a name; values that are not IDs are passed over. */
export function usePeopleNames(ids: unknown[]): ReadonlyMap<string, string> {
  const found = useQueries({
    queries: ids.filter((id): id is string => typeof id === "string" && id !== "").map((id) => personQuery(id)),
  });
  return new Map(found.flatMap((q) => (q.data?.name ? [[q.data.id, q.data.name] as const] : [])));
}
