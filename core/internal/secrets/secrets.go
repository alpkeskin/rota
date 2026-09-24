// Package secrets encrypts sensitive values (upstream proxy passwords) before
// they are written to the database, and decrypts them after they are read.
//
// Values are sealed with AES-256-GCM and stored as
// "enc:v1:<key id>:<base64(nonce|ciphertext)>", where the key id is a short
// fingerprint of the key that sealed the value. The id lets decryption go
// straight to the right key and lets the startup re-encryption pass select,
// in SQL, only the rows not yet sealed with the primary key.
//
// Anything that is not structurally a sealed value is treated as legacy
// plaintext and returned unchanged, so rows written before encryption was
// introduced keep working until they are re-encrypted at startup. That
// includes a plaintext password which merely starts with "enc:v1:".
//
// The keyring is process-global because encrypted columns are read from many
// packages (repositories, proxy selectors, health checkers). Configure it once
// at startup with SetKeys before any database reads.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

// Prefix marks a value produced by Encrypt.
const Prefix = "enc:v1:"

var (
	// ErrNoKey is returned by Encrypt when no keyring has been configured.
	ErrNoKey = errors.New("secrets: encryption key not configured")
	// ErrDecrypt is returned when no configured key can open a sealed value.
	ErrDecrypt = errors.New("secrets: unable to decrypt value with any configured key")
)

type key struct {
	id   string
	aead cipher.AEAD
}

type keyring struct {
	primary key
	all     []key // primary first, then fallbacks in order
}

// keyIDLen is the length of the hex key fingerprint in a sealed value.
const keyIDLen = 8

var (
	mu   sync.RWMutex
	ring *keyring

	decryptFailures atomic.Int64
	onDecryptError  atomic.Pointer[func(error)]
)

// DeriveKey turns an operator-supplied secret of any length into a 32-byte
// AES-256 key. Operators should supply a high-entropy value
// (e.g. `openssl rand -base64 32`).
func DeriveKey(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func newKey(raw []byte) (key, error) {
	if len(raw) != 32 {
		return key{}, fmt.Errorf("secrets: key must be 32 bytes, got %d", len(raw))
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return key{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return key{}, err
	}
	// Domain-separated fingerprint: identifies the key without being usable
	// to recover it (32 bits of a hash of a 256-bit key).
	sum := sha256.Sum256(append([]byte("rota-secrets-key-id:"), raw...))
	return key{id: hex.EncodeToString(sum[:])[:keyIDLen], aead: aead}, nil
}

// SetKeys configures the keyring. primary encrypts new values; primary and
// every fallback key are tried, in order, when decrypting. Fallback keys allow
// key rotation: data sealed with an old key stays readable until it is
// re-encrypted with the primary.
func SetKeys(primary []byte, fallback ...[]byte) error {
	p, err := newKey(primary)
	if err != nil {
		return err
	}
	kr := &keyring{primary: p, all: []key{p}}
	for _, raw := range fallback {
		k, err := newKey(raw)
		if err != nil {
			return err
		}
		kr.all = append(kr.all, k)
	}
	mu.Lock()
	ring = kr
	mu.Unlock()
	return nil
}

// Reset clears the keyring. Intended for tests.
func Reset() {
	mu.Lock()
	ring = nil
	mu.Unlock()
}

func current() *keyring {
	mu.RLock()
	defer mu.RUnlock()
	return ring
}

// PrimaryKeyID returns the fingerprint of the primary key, or "" when no
// keyring is configured.
func PrimaryKeyID() string {
	kr := current()
	if kr == nil {
		return ""
	}
	return kr.primary.id
}

// PrimarySealedPrefix is the prefix of every value sealed with the primary
// key, for selecting rows that still need re-encryption.
func PrimarySealedPrefix() string {
	id := PrimaryKeyID()
	if id == "" {
		return ""
	}
	return Prefix + id + ":"
}

// minSealedLen is nonce + GCM tag: the smallest possible sealed payload.
const minSealedLen = 12 + 16

// parse splits a sealed value into key id and payload. ok is false when s is
// not structurally a sealed value, i.e. it is legacy plaintext.
func parse(s string) (kid string, payload []byte, ok bool) {
	rest, found := strings.CutPrefix(s, Prefix)
	if !found {
		return "", nil, false
	}
	kid, b64, found := strings.Cut(rest, ":")
	if !found || len(kid) != keyIDLen {
		return "", nil, false
	}
	if _, err := hex.DecodeString(kid); err != nil {
		return "", nil, false
	}
	payload, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(payload) < minSealedLen {
		return "", nil, false
	}
	return kid, payload, true
}

// IsEncrypted reports whether s is structurally a sealed value.
func IsEncrypted(s string) bool {
	_, _, ok := parse(s)
	return ok
}

// Encrypt seals plaintext with the primary key. The empty string is returned
// unchanged so "no password" stays distinguishable in the database.
func Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	kr := current()
	if kr == nil {
		return "", ErrNoKey
	}
	nonce := make([]byte, kr.primary.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secrets: generate nonce: %w", err)
	}
	sealed := kr.primary.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return Prefix + kr.primary.id + ":" + base64.StdEncoding.EncodeToString(sealed), nil
}

// open decrypts a parsed sealed value with the key(s) whose id matches and
// reports whether the primary key was the one that opened it.
func open(kr *keyring, kid string, payload []byte) (plaintext string, usedPrimary bool, err error) {
	for i, k := range kr.all {
		if k.id != kid {
			continue // fingerprints only collide by chance; skip non-matching keys
		}
		ns := k.aead.NonceSize()
		pt, err := k.aead.Open(nil, payload[:ns], payload[ns:], nil)
		if err == nil {
			return string(pt), i == 0, nil
		}
	}
	return "", false, ErrDecrypt
}

// Decrypt opens a value produced by Encrypt. Values that are not sealed are
// legacy plaintext and are returned as-is.
func Decrypt(value string) (string, error) {
	kid, payload, ok := parse(value)
	if !ok {
		return value, nil
	}
	kr := current()
	if kr == nil {
		return "", ErrNoKey
	}
	pt, _, err := open(kr, kid, payload)
	return pt, err
}

// NeedsReencrypt reports whether a stored value should be rewritten with the
// primary key: legacy plaintext, or ciphertext sealed with a fallback key.
// It returns the plaintext so the caller can re-seal it. An error means the
// value could not be decrypted with any configured key.
func NeedsReencrypt(value string) (plaintext string, needed bool, err error) {
	if value == "" {
		return "", false, nil
	}
	kid, payload, ok := parse(value)
	if !ok {
		return value, true, nil
	}
	kr := current()
	if kr == nil {
		return "", false, ErrNoKey
	}
	pt, usedPrimary, err := open(kr, kid, payload)
	if err != nil {
		return "", false, err
	}
	return pt, !usedPrimary, nil
}

// EncryptPtr encrypts *p, preserving nil (which callers use to mean
// "leave the stored value unchanged").
func EncryptPtr(p *string) (*string, error) {
	if p == nil {
		return nil, nil
	}
	enc, err := Encrypt(*p)
	if err != nil {
		return nil, err
	}
	return &enc, nil
}

// DecryptInPlace replaces *p with its plaintext after a database scan. A value
// that cannot be decrypted is cleared (set to nil) rather than handed to a
// dialer as a bogus password; the failure is counted and reported through
// the hook registered with OnDecryptError.
func DecryptInPlace(p **string) {
	if p == nil || *p == nil {
		return
	}
	pt, err := Decrypt(**p)
	if err != nil {
		decryptFailures.Add(1)
		if fn := onDecryptError.Load(); fn != nil {
			(*fn)(err)
		}
		*p = nil
		return
	}
	*p = &pt
}

// OnDecryptError registers a callback invoked whenever DecryptInPlace fails.
func OnDecryptError(fn func(error)) {
	if fn == nil {
		onDecryptError.Store(nil)
		return
	}
	onDecryptError.Store(&fn)
}

// DecryptFailures returns the number of values DecryptInPlace failed to open.
func DecryptFailures() int64 {
	return decryptFailures.Load()
}
