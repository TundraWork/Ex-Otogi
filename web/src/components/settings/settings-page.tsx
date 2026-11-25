import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ArrowLeft } from "lucide-react";
import { type ReactNode, Suspense } from "react";
import { PendingRestart } from "@/components/pending-restart";
import { Tile } from "@/components/tile";
import { Skeleton } from "@/components/ui/skeleton";
import type { sectionPages, settingsPages } from "@/lib/settings";
import { addingOf, type Review, SettingsCard, type SettingsSearch } from "./settings-card";

/** SettingsPage is a page under Settings: its heading, as the Settings list
 * names it, what the page is about, and an action beside them. */
export function SettingsPage({
  page,
  about,
  action,
  children,
}: {
  page: (typeof settingsPages)[number];
  about: ReactNode;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="min-w-0 px-5 py-6 sm:px-10 sm:py-7">
      <Link
        to="/settings"
        activeOptions={{ exact: true }}
        className="hit-target relative mb-4 inline-flex items-center gap-1 rounded-md text-muted-foreground text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring md:hidden"
      >
        <ArrowLeft className="size-4" /> Settings
      </Link>
      {/* On a phone the tile and the title share a line, and what the page is about runs under both. */}
      <div className="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-x-4 gap-y-3 sm:grid-cols-[auto_minmax(0,1fr)_auto] sm:items-start sm:gap-y-0">
        <div className="sm:row-span-2">
          <Tile tone={page.tone} icon={page.icon} size="lg" />
        </div>
        <h1 className="font-bold text-2xl leading-tight tracking-[-0.035em] sm:text-[1.875rem]">{page.title}</h1>
        <p className="col-span-full max-w-[70ch] text-muted-foreground text-sm sm:col-span-1 sm:col-start-2">{about}</p>
        {action && (
          <div className="col-span-full sm:col-span-1 sm:col-start-3 sm:row-span-2 sm:row-start-1">{action}</div>
        )}
      </div>
      {children}
    </div>
  );
}

/** SectionPage edits one section of the settings outside modules, with the
 * settings card modules have. One the bot reads when it starts says, above
 * the card, when changes are waiting for a restart. */
export function SectionPage({
  page,
  about,
  review,
  children,
}: {
  page: (typeof sectionPages)[number];
  about: ReactNode;
  review?: Review;
  /** children follow the settings card. */
  children?: ReactNode;
}) {
  const applies = "applies" in page ? page.applies : undefined;
  // Each section's route takes settingsSearch.
  const search = useSearch({ strict: false }) as SettingsSearch;
  const navigate = useNavigate();
  return (
    <SettingsPage page={page} about={about}>
      <div className="mt-6 flex max-w-4xl flex-col gap-4">
        {applies && <PendingRestart />}
        <Suspense fallback={<Skeleton className="h-96 w-full rounded-2xl" />}>
          <SettingsCard
            of={{ section: page.section }}
            effect={page.effect}
            applies={applies}
            review={review}
            adding={addingOf(search)}
            onAdded={() => void navigate({ to: ".", search: {}, replace: true, resetScroll: false })}
          />
        </Suspense>
        {children}
      </div>
    </SettingsPage>
  );
}
