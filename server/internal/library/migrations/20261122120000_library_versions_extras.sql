-- Versions and extras (LIB-3). Extras are trailers, featurettes and the like,
-- attached to their movie or show. ordinal, seq and fresh number and adopt
-- versions (LIB-3 Task 3); flags record a version the admin may compare
-- against another of the same item.

CREATE TABLE library_extras (
    id TEXT PRIMARY KEY,
    item_id TEXT NOT NULL REFERENCES library_items(id) ON DELETE CASCADE,
    file_id TEXT NOT NULL UNIQUE REFERENCES library_files(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('trailer','featurette','behindTheScenes','deletedScene','interview','scene','short','other')),
    title TEXT NOT NULL
) STRICT;
CREATE INDEX library_extras_item ON library_extras(item_id);
ALTER TABLE library_versions ADD COLUMN ordinal INTEGER NOT NULL DEFAULT 1; -- order within a label group: flag target and the versionOrderSQL tie-break; never shown (Task 4 ranks at read time) and never used to decide which file is newer
ALTER TABLE library_versions ADD COLUMN seq INTEGER NOT NULL DEFAULT 0;     -- placement order within the item, never reused; 0 = not yet numbered by Task 3's BackfillVersions
ALTER TABLE library_versions ADD COLUMN fresh INTEGER NOT NULL DEFAULT 0; -- placed since the last whole-root scan; see Task 3
CREATE INDEX library_versions_fresh ON library_versions(file_id) WHERE fresh = 1;
CREATE TABLE library_version_flags (
    version_id TEXT PRIMARY KEY REFERENCES library_versions(id) ON DELETE CASCADE,
    of_id TEXT NOT NULL REFERENCES library_versions(id) ON DELETE CASCADE,
    state TEXT NOT NULL CHECK (state IN ('flagged','kept'))
) STRICT;
CREATE INDEX library_version_flags_of ON library_version_flags(of_id);
