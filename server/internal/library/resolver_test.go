package library

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/playback"
)

var _ playback.Resolver = (*Resolver)(nil)

// viewer is the principal M1Access grants everything to.
var viewer = contracts.Principal{AccountID: "acc", ProfileID: "prof"}

// versionOf returns an item's only version.
func (e *benv) versionOf(itemID string) contracts.MediaVersion {
	e.t.Helper()
	d := e.detail(grantAll, itemID)
	if len(d.Versions) != 1 {
		e.t.Fatalf("versions %+v", d.Versions)
	}
	return d.Versions[0]
}

func TestResolverResolvesCatalogueFile(t *testing.T) {
	e := newBrowse(t)
	e.library("Movies", "movies", []string{"Movies"}, "Movies/A (2001)/A (2001).mkv")
	item := e.itemID("A")
	v := e.versionOf(item)

	src, err := NewResolver(e.s, M1Access(m1Viewer())).Resolve(viewer, item, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := playback.Source{ItemID: item, VersionID: v.ID, FileID: v.FileID,
		RelativePath: "Movies/A (2001)/A (2001).mkv", SourceRevision: e.revisionOf(v.FileID)}
	if src != want {
		t.Fatalf("got %+v\nwant %+v", src, want)
	}
	// The revision is the one playback computes from the file itself.
	info, err := os.Stat(filepath.Join(e.m.dir, src.RelativePath))
	if err != nil {
		t.Fatal(err)
	}
	if playback.Revision(src.FileID, info) != src.SourceRevision {
		t.Fatalf("revision %s doesn't match the file", src.SourceRevision)
	}
}

func TestResolverRefusesOfflineRoot(t *testing.T) {
	e := newBrowse(t)
	lib := e.library("Movies", "movies", []string{"Movies"}, "Movies/A (2001)/A (2001).mkv", "Movies/B (2002)/B (2002).mkv")
	a, b := e.itemID("A"), e.itemID("B")
	va, vb := e.versionOf(a), e.versionOf(b)
	r := NewResolver(e.s, M1Access(m1Viewer()))
	refused := func(name string, p contracts.Principal, item, version string) {
		t.Helper()
		if _, err := r.Resolve(p, item, version); !errors.Is(err, playback.ErrNotFound) {
			t.Fatalf("%s: %v, want playback.ErrNotFound", name, err)
		}
	}

	refused("wrong item for the version", viewer, a, vb.ID)
	refused("unknown version", viewer, a, "ver-unknown")
	refused("unknown item", viewer, "item-unknown", va.ID)
	refused("ungranted library", contracts.Principal{AccountID: "other", ProfileID: "prof"}, a, va.ID)
	only := NewResolver(e.s, func(contracts.Principal) Grant { return Grant{LibraryIDs: []string{"lib-other"}} })
	if _, err := only.Resolve(viewer, a, va.ID); !errors.Is(err, playback.ErrNotFound) {
		t.Fatalf("other library's grant: %v", err)
	}

	// A missing file is refused; the other still plays.
	if err := os.Remove(filepath.Join(e.m.dir, "Movies/B (2002)/B (2002).mkv")); err != nil {
		t.Fatal(err)
	}
	e.scan(lib.Roots[0].ID)
	refused("file missing", viewer, b, vb.ID)
	if _, err := r.Resolve(viewer, a, va.ID); err != nil {
		t.Fatalf("A still present: %v", err)
	}

	// An offline root refuses everything on it; degraded still plays.
	set := func(state string) {
		if _, err := e.s.db.Exec(`UPDATE library_roots SET state = ? WHERE id = ?`, state, lib.Roots[0].ID); err != nil {
			t.Fatal(err)
		}
	}
	set("offline")
	refused("root offline", viewer, a, va.ID)
	set("degraded")
	if _, err := r.Resolve(viewer, a, va.ID); err != nil {
		t.Fatalf("degraded root: %v", err)
	}
}
