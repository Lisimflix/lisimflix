package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

// Item returns one item with its genres and, for a movie or episode, every
// version in LIB-2c decision 10's order (the first is the default, whose
// duration the summary carries). An item outside the grant is ErrNotFound,
// the same as an unknown ID. META-1c fills the descriptive fields; until then
// they are null or empty, and externalIds stays empty.
// Planned: LIB-2c (the catalogue API serves it).
func (s *Store) Item(ctx context.Context, g Grant, itemID string) (contracts.ItemDetail, error) {
	d := contracts.ItemDetail{Genres: []string{}, ExternalIDs: []contracts.ExternalID{}, Versions: []contracts.MediaVersion{},
		Directors: []string{}, Studios: []string{}}
	err := s.read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+itemColumns+itemJoins+` WHERE i.id = ?`, itemID)
		if err != nil {
			return err
		}
		defer rows.Close()
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				return err
			}
			return ErrNotFound
		}
		it, _, err := scanItem(rows, 0)
		if err != nil {
			return err
		}
		rows.Close()
		if !g.allows(it.LibraryID) {
			return ErrNotFound
		}
		d.Item = it
		genres, err := queryStrings(ctx, tx, `SELECT genre FROM library_item_genres WHERE item_id = ? ORDER BY genre`, itemID)
		if err != nil {
			return err
		}
		d.Genres = append(d.Genres, genres...)
		if it.Kind == contracts.KindMovie || it.Kind == contracts.KindEpisode {
			d.Versions, err = itemVersions(ctx, tx, g, itemID)
		}
		return err
	})
	if err != nil {
		return contracts.ItemDetail{}, err
	}
	return d, nil
}

// itemVersions is the one read path for an item's versions: every version,
// available or not, ordered by versionOrderSQL, so the first is the default
// that listItems' durationMs also reads. Streams and the container come from
// the stored probe (none before the first probe); fileName is the file's bare
// name for admins and null for everyone else. Segment stays null until LIB-3
// locates shared-file episodes here.
func itemVersions(ctx context.Context, tx *sql.Tx, g Grant, itemID string) ([]contracts.MediaVersion, error) {
	rows, err := tx.QueryContext(ctx, `SELECT v.id, v.file_id, v.label, CASE WHEN f.duration_ms > 0 THEN f.duration_ms END,
		f.size, f.mtime_ns, f.path, f.probe_json, (f.state = 'present' AND r.state != 'offline')
		FROM library_versions v JOIN library_files f ON f.id = v.file_id JOIN library_roots r ON r.id = f.root_id
		WHERE v.item_id = ? ORDER BY `+versionOrderSQL, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []contracts.MediaVersion{}
	for rows.Next() {
		var v contracts.MediaVersion
		var label, probeJSON sql.NullString
		var duration sql.NullInt64
		var size, mtime int64
		var rel string
		if err := rows.Scan(&v.ID, &v.FileID, &label, &duration, &size, &mtime, &rel, &probeJSON, &v.Available); err != nil {
			return nil, err
		}
		if label.Valid {
			v.Label = &label.String
		}
		if duration.Valid {
			v.DurationMs = &duration.Int64
		}
		v.SizeBytes = &size
		v.SourceRevision = contracts.SourceRevision(v.FileID, size, mtime)
		if probeJSON.Valid {
			var r probe.Result
			if err := json.Unmarshal([]byte(probeJSON.String), &r); err != nil {
				return nil, fmt.Errorf("library: file %s probe: %w", v.FileID, err)
			}
			v.Container = r.Container
			v.Video, v.Audio, v.Subtitles = streams(r)
		}
		if g.Admin {
			v.FileName = fileName(rel)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// fileName is a file's bare name, or nil when the name can't be served as
// one (the contract forbids / and \ in it).
func fileName(rel string) *string {
	name := path.Base(rel)
	if name == "." || name == "/" || strings.ContainsAny(name, `/\`) {
		return nil
	}
	return &name
}
