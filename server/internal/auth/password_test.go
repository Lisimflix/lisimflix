package auth

import (
	"strings"
	"testing"
)

func TestPasswordVerification(t *testing.T) {
	h, err := HashPassword([]byte("correct horse battery"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("hash = %q, want M1 default parameters", h)
	}
	if !VerifyPassword(h, []byte("correct horse battery")) {
		t.Fatal("correct password rejected")
	}
	for _, wrong := range []string{"correct horse batterY", "", "correct horse battery "} {
		if VerifyPassword(h, []byte(wrong)) {
			t.Fatalf("wrong password %q accepted", wrong)
		}
	}
	h2, _ := HashPassword([]byte("correct horse battery"))
	if h2 == h {
		t.Fatal("two hashes of one password share a salt")
	}
	for _, bad := range []string{"short", strings.Repeat("x", 1025)} {
		if _, err := HashPassword([]byte(bad)); err == nil {
			t.Fatalf("HashPassword accepted a %d-byte password", len(bad))
		}
	}
	if VerifyPassword(h, []byte(strings.Repeat("x", 1025))) {
		t.Fatal("oversized password verified")
	}
}

func TestHostileHashParametersAreRejected(t *testing.T) {
	var calls int
	defer stubArgon(t, &calls)()
	const salt, key = "c2FsdHNhbHRzYWx0c2FsdA", "a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"
	for name, h := range map[string]string{
		"argon2i":          "$argon2i$v=19$m=19456,t=2,p=1$" + salt + "$" + key,
		"old version":      "$argon2id$v=16$m=19456,t=2,p=1$" + salt + "$" + key,
		"memory over 64Mi": "$argon2id$v=19$m=65537,t=2,p=1$" + salt + "$" + key,
		"huge memory":      "$argon2id$v=19$m=4294967295,t=2,p=1$" + salt + "$" + key,
		"huge time":        "$argon2id$v=19$m=19456,t=100000,p=1$" + salt + "$" + key,
		"huge parallelism": "$argon2id$v=19$m=19456,t=2,p=255$" + salt + "$" + key,
		"zero time":        "$argon2id$v=19$m=19456,t=0,p=1$" + salt + "$" + key,
		"short salt":       "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$" + key,
		"short key":        "$argon2id$v=19$m=19456,t=2,p=1$" + salt + "$a2V5",
		"padded base64":    "$argon2id$v=19$m=19456,t=2,p=1$" + salt + "==$" + key,
		"extra field":      "$argon2id$v=19$m=19456,t=2,p=1,x=1$" + salt + "$" + key,
		"reordered":        "$argon2id$v=19$t=2,m=19456,p=1$" + salt + "$" + key,
		"leading zero":     "$argon2id$v=19$m=019456,t=2,p=1$" + salt + "$" + key,
		"trailing segment": "$argon2id$v=19$m=19456,t=2,p=1$" + salt + "$" + key + "$x",
		"empty":            "",
	} {
		if VerifyPassword(h, []byte("correct horse battery")) {
			t.Errorf("%s: verified", name)
		}
		if _, err := parseHash(h); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
	if calls != 0 {
		t.Fatalf("argon2 ran %d times on rejected hashes; parsing must reject before allocation", calls)
	}
}

// stubArgon counts argon2 calls without doing the work.
func stubArgon(t *testing.T, calls *int) func() {
	t.Helper()
	old := argonKey
	argonKey = func(pw, salt []byte, time, memory uint32, threads uint8, keyLen uint32) []byte {
		*calls++
		return make([]byte, keyLen)
	}
	return func() { argonKey = old }
}
