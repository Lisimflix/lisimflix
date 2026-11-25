package library

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"path"
	"slices"
	"strings"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/probe"
	"github.com/Lisimflix/lisimflix/server/internal/textfold"
)

// Item returns one item with its genres, a movie's or show's extras, and, for
// a movie or episode, every listed version in LIB-2c decision 10's order (the
// first is the default, whose duration the summary carries); LIB-3 decision 2
// hides a missing version beside a present one. An item outside the grant is ErrNotFound,
// the same as an unknown ID. META-1c fills the descriptive fields; until then
// they are null or empty, and externalIds stays empty.
func (s *Store) Item(ctx context.Context, g Grant, itemID string) (contracts.ItemDetail, error) {
	d := contracts.ItemDetail{Genres: []string{}, ExternalIDs: []contracts.ExternalID{}, Versions: []contracts.MediaVersion{},
		Extras: []contracts.Extra{}, Directors: []string{}, Studios: []string{}}
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
			if d.Versions, err = s.itemVersions(ctx, tx, g, itemID); err != nil {
				return err
			}
		}
		if it.Kind == contracts.KindMovie || it.Kind == contracts.KindShow {
			d.Extras, err = s.itemExtras(ctx, tx, g, itemID)
		}
		return err
	})
	if err != nil {
		return contracts.ItemDetail{}, err
	}
	return d, nil
}

// itemVersions is the one read path for an item's versions: every live
// version (not retired), ordered by versionOrderSQL, so the first is the
// default that listItems' durationMs also reads. A version whose file is
// missing is left out while the item has a present version on a root that
// isn't offline (decision 2). Labels are numbered here, by rank among the
// listed versions of the same label (decision 6e's fold), so the first copy
// reads plain. Streams and the container come from the stored probe (none
// before the first probe); fileName is the file's bare name for admins and
// null for everyone else. Segment is set only for an episode sharing its file
// with other episodes of its season, and only from a probe of the file's
// current revision (decision 6d).
func (s *Store) itemVersions(ctx context.Context, tx *sql.Tx, g Grant, itemID string) ([]contracts.MediaVersion, error) {
	// The hide rule sits in WHERE so hidden versions use up no rank.
	rows, err := tx.QueryContext(ctx, `SELECT v.id, v.file_id, v.label, CASE WHEN f.duration_ms > 0 THEN f.duration_ms END,
		f.size, f.mtime_ns, f.path, f.probe_json, (f.state = 'present' AND r.state != 'offline'),
		COALESCE(f.probe_size = f.size AND f.probe_mtime_ns = f.mtime_ns, 0), CASE WHEN i.kind = 'episode' THEN i.parent_id END,
		ROW_NUMBER() OVER (PARTITION BY lower(v.label) ORDER BY `+versionOrderSQL+`)
		FROM library_versions v JOIN library_files f ON f.id = v.file_id JOIN library_roots r ON r.id = f.root_id
		JOIN library_items i ON i.id = v.item_id
		WHERE v.item_id = ? AND `+liveVersion+` AND NOT (f.state = 'missing' AND r.state != 'offline' AND EXISTS (
			SELECT 1 FROM library_versions pv JOIN library_files pf ON pf.id = pv.file_id JOIN library_roots pr ON pr.id = pf.root_id
			WHERE pv.item_id = v.item_id AND pf.state = 'present' AND pr.state != 'offline'))
		ORDER BY `+versionOrderSQL, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type located struct {
		at     int // index in out
		season string
		probe  *probe.Result
	}
	var shared []located
	out := []contracts.MediaVersion{}
	for rows.Next() {
		var v contracts.MediaVersion
		var f fileCols
		var label, season sql.NullString
		var fresh bool
		var rank int
		if err := rows.Scan(&v.ID, &v.FileID, &label, &f.duration, &f.size, &f.mtime, &f.rel, &f.probeJSON, &v.Available,
			&fresh, &season, &rank); err != nil {
			return nil, err
		}
		if label.Valid {
			v.Label = &label.String
		}
		v.Label = displayLabel(v.Label, rank)
		r := s.fileVersion(&v, g, f)
		if fresh && season.Valid && r != nil && len(r.Chapters) >= 2 {
			shared = append(shared, located{len(out), season.String, r})
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for _, l := range shared {
		eps, err := queryStrings(ctx, tx, `SELECT v.item_id FROM library_versions v JOIN library_items i ON i.id = v.item_id
			WHERE v.file_id = ? AND i.kind = 'episode' AND i.parent_id = ? ORDER BY i.idx, i.air_date, i.id`, out[l.at].FileID, l.season)
		if err != nil {
			return nil, err
		}
		if i := slices.Index(eps, itemID); i >= 0 {
			out[l.at].Segment = SegmentOf(l.probe.Chapters, l.probe.DurationMs, i, len(eps))
		}
	}
	return out, nil
}

// itemExtras lists a movie's or show's extras (none for a season or episode),
// each played by its own ID (decision 4). A retired file's extra is gone, and
// a missing one is left out unless its root is offline, so a renamed extra
// doesn't list twice.
func (s *Store) itemExtras(ctx context.Context, tx *sql.Tx, g Grant, itemID string) ([]contracts.Extra, error) {
	rows, err := tx.QueryContext(ctx, `SELECT x.id, x.type, x.title, x.file_id, CASE WHEN f.duration_ms > 0 THEN f.duration_ms END,
		f.size, f.mtime_ns, f.path, f.probe_json, (f.state = 'present' AND r.state != 'offline')
		FROM library_extras x JOIN library_items i ON i.id = x.item_id
		JOIN library_files f ON f.id = x.file_id JOIN library_roots r ON r.id = f.root_id
		WHERE x.item_id = ? AND i.kind IN ('movie', 'show') AND `+liveVersion+` AND (f.state = 'present' OR r.state = 'offline')
		ORDER BY x.type, x.title, x.id`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []contracts.Extra{}
	for rows.Next() {
		var x contracts.Extra
		var f fileCols
		if err := rows.Scan(&x.ID, &x.Type, &x.Title, &x.Version.FileID, &f.duration, &f.size, &f.mtime, &f.rel, &f.probeJSON, &x.Version.Available); err != nil {
			return nil, err
		}
		x.Version.ID = x.ID
		s.fileVersion(&x.Version, g, f)
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Titles compare folded, as item titles do: "behind the camera" before "Zebra Unit" (#238 review).
	slices.SortStableFunc(out, func(a, b contracts.Extra) int {
		return cmp.Or(strings.Compare(string(a.Type), string(b.Type)), strings.Compare(textfold.Sort(a.Title), textfold.Sort(b.Title)), strings.Compare(a.ID, b.ID))
	})
	return out, nil
}

// fileCols are a version's or extra's file columns as read.
type fileCols struct {
	duration  sql.NullInt64
	size      int64
	mtime     int64
	rel       string
	probeJSON sql.NullString
}

// fileVersion fills v from its file: duration, size, revision, the stored
// probe's container and streams, and fileName for admins. It returns the
// stored probe, or nil when there is none or it can't be read.
func (s *Store) fileVersion(v *contracts.MediaVersion, g Grant, f fileCols) *probe.Result {
	if f.duration.Valid {
		v.DurationMs = &f.duration.Int64
	}
	v.SizeBytes = &f.size
	v.SourceRevision = contracts.SourceRevision(v.FileID, f.size, f.mtime)
	if g.Admin {
		v.FileName = fileName(f.rel)
	}
	if !f.probeJSON.Valid {
		return nil
	}
	var r probe.Result
	if err := json.Unmarshal([]byte(f.probeJSON.String), &r); err != nil {
		// One bad probe shouldn't fail the whole item: serve the version bare.
		s.log.Error("library.probe_corrupt", "file", v.FileID, "err", err)
		return nil
	}
	v.Container = r.Container
	v.Video, v.Audio, v.Subtitles = streams(r)
	return &r
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
