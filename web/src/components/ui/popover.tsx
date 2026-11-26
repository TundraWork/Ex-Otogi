"use client";

import { Popover as PopoverPrimitive } from "@base-ui/react/popover";
import type * as React from "react";
import { cn } from "@/lib/utils";

export const Popover: typeof PopoverPrimitive.Root = PopoverPrimitive.Root;

export function PopoverTrigger({ className, children, ...props }: PopoverPrimitive.Trigger.Props): React.ReactElement {
  return (
    <PopoverPrimitive.Trigger className={className} data-slot="popover-trigger" {...props}>
      {children}
    </PopoverPrimitive.Trigger>
  );
}

/** PopoverPopup is the floating panel, styled as the menu's and the
 * select's are. */
export function PopoverPopup({
  children,
  className,
  sideOffset = 4,
  align = "start",
  side = "bottom",
  ...props
}: PopoverPrimitive.Popup.Props & {
  align?: PopoverPrimitive.Positioner.Props["align"];
  sideOffset?: PopoverPrimitive.Positioner.Props["sideOffset"];
  side?: PopoverPrimitive.Positioner.Props["side"];
}): React.ReactElement {
  return (
    <PopoverPrimitive.Portal>
      <PopoverPrimitive.Positioner
        align={align}
        className="z-50"
        data-slot="popover-positioner"
        side={side}
        sideOffset={sideOffset}
      >
        <PopoverPrimitive.Popup
          className={cn(
            "relative flex origin-(--transform-origin) flex-col rounded-lg border bg-popover bg-clip-padding shadow-lg/5 outline-none before:pointer-events-none before:absolute before:inset-0 before:rounded-[calc(var(--radius-lg)-1px)] before:shadow-[0_1px_--theme(--color-black/4%)] focus:outline-none",
            className,
          )}
          data-slot="popover-popup"
          {...props}
        >
          {children}
        </PopoverPrimitive.Popup>
      </PopoverPrimitive.Positioner>
    </PopoverPrimitive.Portal>
  );
}

export const PopoverClose: typeof PopoverPrimitive.Close = PopoverPrimitive.Close;

export function PopoverTitle({ className, ...props }: PopoverPrimitive.Title.Props): React.ReactElement {
  return (
    <PopoverPrimitive.Title className={cn("font-medium text-sm", className)} data-slot="popover-title" {...props} />
  );
}
