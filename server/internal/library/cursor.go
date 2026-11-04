package library

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
)

// maxCursor is the longest cursor the API accepts (LIB-2c decision 8).
const maxCursor = 512

type cursorBody struct {
	V int    `json:"v"`
	Q string `json:"q"`
	K []any  `json:"k"`
}

func queryHash(q string) string {
	h := sha256.Sum256([]byte(q))
	return base64.RawURLEncoding.EncodeToString(h[:8])
}

// encodeCursor builds an opaque keyset cursor: base64url JSON holding the
// version, the first 8 bytes of SHA-256 of the canonical query q, and the sort
// keys of the last item returned (the item ID last).
// Planned: LIB-2c (listItems paging, in the catalogue API).
func encodeCursor(q string, keys ...any) string {
	if keys == nil {
		keys = []any{}
	}
	b, err := json.Marshal(cursorBody{V: 1, Q: queryHash(q), K: keys})
	if err != nil {
		panic(err) // keys are strings, integers and nil
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor returns the keys of a cursor made for query q. Integers come
// back as int64, other numbers as float64, strings and nil as themselves. A
// cursor that doesn't decode, is over 512 bytes, or was made by another
// version or query is an ErrBadRequest.
// Planned: LIB-2c (listItems paging, in the catalogue API).
func decodeCursor(c, q string) ([]any, error) {
	if len(c) > maxCursor {
		return nil, fmt.Errorf("%w: cursor is too long", ErrBadRequest)
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return nil, fmt.Errorf("%w: cursor is not valid", ErrBadRequest)
	}
	var body cursorBody
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil || body.K == nil {
		return nil, fmt.Errorf("%w: cursor is not valid", ErrBadRequest)
	}
	if body.V != 1 {
		return nil, fmt.Errorf("%w: cursor version isn't supported", ErrBadRequest)
	}
	if body.Q != queryHash(q) {
		return nil, fmt.Errorf("%w: cursor belongs to a different query", ErrBadRequest)
	}
	for i, k := range body.K {
		if n, ok := k.(json.Number); ok {
			if v, err := strconv.ParseInt(n.String(), 10, 64); err == nil {
				body.K[i] = v
			} else if f, err := n.Float64(); err == nil {
				body.K[i] = f
			} else {
				return nil, fmt.Errorf("%w: cursor is not valid", ErrBadRequest)
			}
		}
	}
	return body.K, nil
}
