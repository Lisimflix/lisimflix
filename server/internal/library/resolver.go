package library

import (
	"context"
	"database/sql"
	"errors"
	"path"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/playback"
)

// Resolver plays catalogue files: it maps an item and one of its versions to
// the version's file (playback.Resolver). Playback calls it on every session
// start and every range request, so it is one indexed query.
type Resolver struct {
	st     *Store
	access Access
}

// NewResolver resolves st's versions for the principals access allows.
func NewResolver(st *Store, access Access) *Resolver {
	return &Resolver{st: st, access: access}
}

// Resolve implements playback.Resolver. It answers playback.ErrNotFound, the
// same as an unknown ID, when the item isn't in a granted library, the version
// isn't the item's, the file isn't present or its root is offline.
func (r *Resolver) Resolve(p contracts.Principal, itemID, versionID string) (playback.Source, error) {
	var libraryID, fileID, rootPath, filePath string
	var size, mtime int64
	err := r.st.db.QueryRowContext(context.Background(), `SELECT i.library_id, f.id, rt.path, f.path, f.size, f.mtime_ns
		FROM library_versions v
		JOIN library_items i ON i.id = v.item_id
		JOIN library_files f ON f.id = v.file_id
		JOIN library_roots rt ON rt.id = f.root_id
		WHERE v.id = ? AND v.item_id = ? AND f.state = 'present' AND rt.state != 'offline'`,
		versionID, itemID).Scan(&libraryID, &fileID, &rootPath, &filePath, &size, &mtime)
	if errors.Is(err, sql.ErrNoRows) {
		return playback.Source{}, playback.ErrNotFound
	}
	if err != nil {
		return playback.Source{}, err
	}
	if !r.access(p).allows(libraryID) {
		return playback.Source{}, playback.ErrNotFound
	}
	return playback.Source{
		ItemID: itemID, VersionID: versionID, FileID: fileID,
		RelativePath:   path.Join(rootPath, filePath), // both slash-separated; the root may be "."
		SourceRevision: contracts.SourceRevision(fileID, size, mtime),
	}, nil
}
