package contracts

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// SourceRevision is the public sourceRevision of one source media file:
// lowercase hex SHA-256 of fileID 0x00 size 0x00 mtimeUnixNano, with decimal
// numbers. It changes exactly when the file's size or modification time
// changes, which is LIB-1's revision rule (library.Revision holds the same two
// values). Playback, the catalogue and progress all compare this string, so
// it must stay byte-for-byte stable. M1's movie uses its movie ID as fileID.
func SourceRevision(fileID string, size, modTimeUnixNano int64) string {
	sum := sha256.Sum256([]byte(fileID + "\x00" + strconv.FormatInt(size, 10) + "\x00" + strconv.FormatInt(modTimeUnixNano, 10)))
	return hex.EncodeToString(sum[:])
}
