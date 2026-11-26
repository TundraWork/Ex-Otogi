import { ExternalLink, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { DialogBackdrop, DialogClose, DialogPortal, DialogPrimitive } from "@/components/ui/dialog";

/** Picture shows an image over the page, as large as the window allows;
 * Escape, the close button, or a click beside it closes it. It goes inside
 * a Dialog whose trigger is the image shown small. */
export function Picture({ src }: { src: string }) {
  return (
    <DialogPortal>
      <DialogBackdrop className="bg-black/80 backdrop-blur-none" />
      <DialogPrimitive.Viewport className="fixed inset-0 z-50 flex items-center justify-center p-4 sm:p-10">
        <DialogPrimitive.Popup className="relative flex max-h-full max-w-full flex-col items-center gap-3 outline-none transition-[scale,opacity] duration-200 data-ending-style:scale-98 data-ending-style:opacity-0 data-starting-style:scale-98 data-starting-style:opacity-0">
          <DialogPrimitive.Title className="sr-only">Picture</DialogPrimitive.Title>
          <img src={src} alt="" className="min-h-0 max-w-full flex-1 rounded-lg object-contain shadow-2xl" />
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              render={<a href={src} target="_blank" rel="noreferrer" />}
              className="bg-background/90"
            >
              <ExternalLink /> Open original
            </Button>
            <DialogClose render={<Button variant="outline" size="sm" className="bg-background/90" />}>
              <X /> Close
            </DialogClose>
          </div>
        </DialogPrimitive.Popup>
      </DialogPrimitive.Viewport>
    </DialogPortal>
  );
}
