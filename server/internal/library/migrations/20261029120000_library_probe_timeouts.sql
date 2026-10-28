-- ffprobe timeouts in a row for a file (#109). A timeout is retried with
-- backoff until it has happened often enough to be kept as a failure; a
-- stored result or the admin's retry resets the count.

ALTER TABLE library_files ADD COLUMN probe_timeouts INTEGER NOT NULL DEFAULT 0;
