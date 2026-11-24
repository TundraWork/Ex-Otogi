import type { ComponentProps, ReactNode } from "react";
import { cn } from "@/lib/utils";

/** Card is the panel's raised card. */
export function Card({ className, ...props }: ComponentProps<"section">) {
  return <section className={cn("surface-raised flow-root rounded-2xl", className)} {...props} />;
}

/** CardHeader titles a card: an optional tile, the title with a note beside
 * it or a description under it, and the card's actions as children. A card
 * the page's heading already names has only the note. */
export function CardHeader({
  icon,
  title,
  note,
  description,
  children,
}: {
  icon?: ReactNode;
  title?: string;
  note?: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <header className="flex items-center gap-3 px-4 pt-3.5 pb-2.5">
      {icon}
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-baseline gap-x-2">
          {title && <h2 className="font-semibold text-[15px] leading-tight">{title}</h2>}
          {note && <span className={cn("text-muted-foreground", title ? "text-xs" : "text-sm")}>{note}</span>}
        </div>
        {description && <p className="text-muted-foreground text-xs">{description}</p>}
      </div>
      {children}
    </header>
  );
}

/** CardInset is the inset panel inside a card that holds its rows. */
export function CardInset({ className, ...props }: ComponentProps<"div">) {
  return <div className={cn("surface-inset mx-3.5 mb-3.5 rounded-xl px-3", className)} {...props} />;
}

/** rows divides a list's rows inside a CardInset. */
export const rows = "divide-y divide-border/80";
