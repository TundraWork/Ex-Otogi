-- +goose Up
-- The MD5 of a sticker's bytes, so a QQ picture, known by the MD5 in its
-- file name and never fetched, is known as a kept sticker. Set when a
-- sticker is kept. Those kept before have none, and are known only by their
-- SHA-256 until it is set by hand, once, where any were kept (EXO-17).
ALTER TABLE stickers ADD COLUMN md5 BLOB;
CREATE INDEX stickers_md5 ON stickers (md5);
