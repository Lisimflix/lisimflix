-- Scoped scans (LIB-2b): a watcher event rescans one folder, not the whole
-- root. scope is the folder or file the run covered, relative to the root;
-- NULL means the whole root. Only whole-root runs count towards retirement.
-- cause says what started the run, for the status view and logs.
ALTER TABLE library_scan_runs ADD COLUMN scope TEXT;
ALTER TABLE library_scan_runs ADD COLUMN cause TEXT NOT NULL DEFAULT 'manual'
    CHECK (cause IN ('startup', 'schedule', 'watch', 'recheck', 'manual', 'added', 'import'));
