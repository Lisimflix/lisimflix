//go:build !linux

package storage

// The server image targets Linux; other OSes are development-only.
func checkLocalFilesystem(string) error { return nil }
