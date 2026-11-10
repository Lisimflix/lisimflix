-- Browsing (LIB-2c): stored sort keys, display values for META-1c, and the
-- facet values derived once from each stored probe. NULL display_* means "use
-- the parsed value". sort_key is the folded sort title (textfold.Title) and
-- sort_group is 1 for a-z, else 0 (the # group); Go writes both and a
-- startup backfill fills rows that predate this migration.

ALTER TABLE library_items ADD COLUMN sort_group INTEGER;
ALTER TABLE library_items ADD COLUMN sort_key TEXT;
ALTER TABLE library_items ADD COLUMN display_title TEXT;
ALTER TABLE library_items ADD COLUMN display_sort_title TEXT;
ALTER TABLE library_items ADD COLUMN display_year INTEGER;

ALTER TABLE library_files ADD COLUMN resolution TEXT CHECK (resolution IN ('sd', '720p', '1080p', '4k'));
ALTER TABLE library_files ADD COLUMN hdr INTEGER CHECK (hdr IN (0, 1));
ALTER TABLE library_files ADD COLUMN duration_ms INTEGER;

CREATE TABLE library_item_genres (
    item_id TEXT NOT NULL REFERENCES library_items(id) ON DELETE CASCADE,
    genre   TEXT NOT NULL,
    PRIMARY KEY (item_id, genre)
) STRICT;

-- Each distinct audio language of a file's stored probe, ISO 639-2/T.
CREATE TABLE library_file_audio (
    file_id TEXT NOT NULL REFERENCES library_files(id) ON DELETE CASCADE,
    lang    TEXT NOT NULL,
    PRIMARY KEY (file_id, lang)
) STRICT;

CREATE INDEX library_items_browse ON library_items(library_id, sort_group, sort_key, id) WHERE parent_id IS NULL;
CREATE INDEX library_items_added ON library_items(library_id, created_at DESC, id) WHERE parent_id IS NULL;
CREATE INDEX library_items_children ON library_items(parent_id, idx, id);
