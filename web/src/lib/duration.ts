// Durations as the bot's settings write them: Go's, such as 720h, 90m, 1h30m.

const units: Record<string, number> = { ns: 1e-6, us: 1e-3, µs: 1e-3, ms: 1, s: 1000, m: 60_000, h: 3_600_000 };

/** milliseconds is how long a duration such as 1h30m is; what is not a duration is undefined. */
export function milliseconds(duration: unknown): number | undefined {
  if (typeof duration !== "string" || !/^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/.test(duration)) return undefined;
  let total = 0;
  for (const [, amount, unit] of duration.matchAll(/([\d.]+)(ns|us|µs|ms|s|m|h)/g)) {
    total += Number(amount) * (units[unit as string] as number);
  }
  return total;
}

/** shorter says whether a duration is being made shorter. One that cannot
 * be read is not: the form refuses it before anything is saved. */
export function shorter(was: unknown, now: unknown): boolean {
  const from = milliseconds(was);
  const to = milliseconds(now);
  return from !== undefined && to !== undefined && to < from;
}

const parts: [string, number][] = [
  ["day", 86_400_000],
  ["hour", 3_600_000],
  ["minute", 60_000],
  ["second", 1000],
  ["millisecond", 1],
];

/** reading says how long a duration is in words: 720h is "30 days", 90m
 * "1 hour 30 minutes"; what is not a duration has none. */
export function reading(duration: unknown): string | undefined {
  let left = milliseconds(duration);
  if (left === undefined) return undefined;
  left = Math.round(left);
  const words: string[] = [];
  for (const [unit, size] of parts) {
    const n = Math.floor(left / size);
    left -= n * size;
    if (n) words.push(`${n} ${unit}${n === 1 ? "" : "s"}`);
  }
  return words.join(" ") || "0 seconds";
}
