package library

import (
	"slices"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/m1config"
)

// Grant is everything the catalogue queries know about the caller: which
// libraries they may see and whether they're an admin. The zero Grant sees
// nothing.
type Grant struct {
	All        bool     // every library
	LibraryIDs []string // otherwise only these
	Admin      bool
}

// allows reports whether the grant covers a library.
func (g Grant) allows(libraryID string) bool {
	return g.All || slices.Contains(g.LibraryIDs, libraryID)
}

// Access decides what a principal may see.
type Access func(contracts.Principal) Grant

// M1Access gives the configured M1 viewer every library and the admin role,
// and anyone else nothing. The M1 account is the owner who ran init and the
// only account auth.Require authenticates today.
// shortcut: one M1 viewer until ACC-2 replaces Access
func M1Access(v m1config.Viewer) Access {
	return func(p contracts.Principal) Grant {
		if p.AccountID != "" && p.AccountID == v.AccountID && p.ProfileID == v.ProfileID {
			return Grant{All: true, Admin: true}
		}
		return Grant{}
	}
}
