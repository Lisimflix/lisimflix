//go:build !m1smoke

package main

import "github.com/Lisimflix/lisimflix/server/internal/storage"

func extraSources() []storage.Source { return nil }
