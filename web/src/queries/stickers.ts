import { queryOptions } from "@tanstack/react-query";
import { api, data, type Schemas } from "@/api/client";

export type Sticker = Schemas["Sticker"];
export type StickerPack = Schemas["StickerPack"];

export const stickersQuery = queryOptions({
  queryKey: ["modules", "media", "stickers"],
  queryFn: () => data(api.GET("/api/modules/media/stickers")),
});

/** pictureURL is where a sticker's picture is served. It never changes for
 * a number, so the browser keeps it. */
export const pictureURL = (id: number) => `/api/modules/media/stickers/${id}/picture`;

/** uploadSticker keeps a picture as a sticker, with no note, in pack unless
 * it is empty. A picture kept already comes back existing when it is put in
 * pack. A refusal is an ApiError whose message is what /sticker add
 * answers. */
export const uploadSticker = (file: Blob, pack: string) =>
  data(
    api.POST("/api/modules/media/stickers", {
      params: { query: pack ? { pack } : {} },
      // The body is the picture's bytes, sent as they are.
      body: file as unknown as string,
      bodySerializer: (body) => body,
      headers: { "Content-Type": "application/octet-stream" },
    }),
  );

export const noteSticker = (id: number, note: string) =>
  data(api.PATCH("/api/modules/media/stickers/{id}", { params: { path: { id } }, body: { note } }));

export const removeSticker = (id: number) =>
  data(api.DELETE("/api/modules/media/stickers/{id}", { params: { path: { id } } }));

export const makePack = (pack: { id: string; title: string; description: string }) =>
  data(api.POST("/api/modules/media/sticker-packs", { body: pack }));

export const editPack = (id: string, title: string, description: string) =>
  data(
    api.PATCH("/api/modules/media/sticker-packs/{pack}", {
      params: { path: { pack: id } },
      body: { title, description },
    }),
  );

export const removePack = (id: string, stickers: "keep" | "remove") =>
  data(api.DELETE("/api/modules/media/sticker-packs/{pack}", { params: { path: { pack: id }, query: { stickers } } }));

export const packSticker = (pack: string, id: number) =>
  data(api.PUT("/api/modules/media/sticker-packs/{pack}/stickers/{id}", { params: { path: { pack, id } } }));

export const unpackSticker = (pack: string, id: number) =>
  data(api.DELETE("/api/modules/media/sticker-packs/{pack}/stickers/{id}", { params: { path: { pack, id } } }));
