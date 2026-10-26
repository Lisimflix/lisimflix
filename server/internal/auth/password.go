// Package auth is the M1 local sign-in: Argon2id passwords, in-memory
// revocable sessions and the three CTR-1 auth routes.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// M1 defaults (OWASP minimum for Argon2id): 19 MiB, 2 passes, 1 lane.
const (
	defaultMemoryKiB = 19 * 1024
	defaultTime      = 2
	defaultThreads   = 1
	saltLen          = 16
	keyLen           = 32

	maxMemoryKiB = 64 * 1024
	maxTime      = 10
	maxThreads   = 4

	MinPasswordBytes = 12
	MaxPasswordBytes = 1024
)

// argonKey is argon2.IDKey; tests replace it to count calls.
var argonKey = argon2.IDKey

type params struct {
	memory, time uint32
	threads      uint8
	salt, key    []byte
}

var errBadHash = errors.New("auth: malformed password hash")

// HashPassword returns an Argon2id PHC string for a 12-1024 byte password.
func HashPassword(password []byte) (string, error) {
	if len(password) < MinPasswordBytes || len(password) > MaxPasswordBytes {
		return "", fmt.Errorf("auth: password must be %d-%d bytes", MinPasswordBytes, MaxPasswordBytes)
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return encode(params{memory: defaultMemoryKiB, time: defaultTime, threads: defaultThreads, salt: salt,
		key: argonKey(password, salt, defaultTime, defaultMemoryKiB, defaultThreads, keyLen)}), nil
}

// VerifyPassword reports whether password matches encoded. A malformed or
// out-of-bounds hash is rejected before any memory is allocated for it.
func VerifyPassword(encoded string, password []byte) bool {
	p, err := parseHash(encoded)
	if err != nil {
		return false
	}
	return verify(p, password)
}

func verify(p params, password []byte) bool {
	if len(password) > MaxPasswordBytes {
		return false
	}
	got := argonKey(password, p.salt, p.time, p.memory, p.threads, uint32(len(p.key)))
	return subtle.ConstantTimeCompare(got, p.key) == 1
}

func encode(p params) string {
	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.memory, p.time, p.threads, b64(p.salt), b64(p.key))
}

// parseHash accepts exactly "$argon2id$v=19$m=M,t=T,p=P$salt$key" with
// unpadded base64 and bounded parameters.
func parseHash(s string) (params, error) {
	if len(s) > 256 {
		return params{}, errBadHash
	}
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return params{}, errBadHash
	}
	kv := strings.Split(parts[3], ",")
	if len(kv) != 3 {
		return params{}, errBadHash
	}
	var vals [3]uint64
	for i, name := range []string{"m=", "t=", "p="} {
		v, ok := strings.CutPrefix(kv[i], name)
		if !ok || v == "" || (len(v) > 1 && v[0] == '0') {
			return params{}, errBadHash
		}
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return params{}, errBadHash
		}
		vals[i] = n
	}
	m, t, p := vals[0], vals[1], vals[2]
	if t < 1 || t > maxTime || p < 1 || p > maxThreads || m < 8*p || m > maxMemoryKiB {
		return params{}, errBadHash
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		return params{}, errBadHash
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > 64 {
		return params{}, errBadHash
	}
	return params{memory: uint32(m), time: uint32(t), threads: uint8(p), salt: salt, key: key}, nil
}
