package secrets

import (
	"errors"
	"strings"
	"testing"
)

func withKeys(t *testing.T, primary []byte, fallback ...[]byte) {
	t.Helper()
	if err := SetKeys(primary, fallback...); err != nil {
		t.Fatalf("SetKeys: %v", err)
	}
	t.Cleanup(Reset)
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	withKeys(t, DeriveKey("k1"))

	for _, pt := range []string{"hunter2", "p@ss:w/rd", "enc:v1:looks-sealed", strings.Repeat("x", 4096), "ünïcødé"} {
		enc, err := Encrypt(pt)
		if err != nil {
			t.Fatalf("Encrypt(%q): %v", pt, err)
		}
		if !IsEncrypted(enc) || strings.Contains(enc, pt) {
			t.Fatalf("Encrypt(%q) = %q, want sealed value not containing plaintext", pt, enc)
		}
		got, err := Decrypt(enc)
		if err != nil || got != pt {
			t.Fatalf("Decrypt(Encrypt(%q)) = %q, %v", pt, got, err)
		}
	}
}

func TestEncryptUsesFreshNonce(t *testing.T) {
	withKeys(t, DeriveKey("k1"))
	a, _ := Encrypt("same")
	b, _ := Encrypt("same")
	if a == b {
		t.Fatal("two encryptions of the same plaintext produced identical ciphertext")
	}
}

func TestEmptyAndLegacyValues(t *testing.T) {
	withKeys(t, DeriveKey("k1"))

	if enc, err := Encrypt(""); err != nil || enc != "" {
		t.Fatalf("Encrypt(\"\") = %q, %v; want empty", enc, err)
	}
	if got, err := Decrypt("legacy-plain"); err != nil || got != "legacy-plain" {
		t.Fatalf("Decrypt(legacy) = %q, %v", got, err)
	}
}

func TestNoKeyConfigured(t *testing.T) {
	Reset()
	if _, err := Encrypt("x"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("Encrypt without key: err = %v, want ErrNoKey", err)
	}
	// Plaintext still reads fine without a key.
	if got, err := Decrypt("plain"); err != nil || got != "plain" {
		t.Fatalf("Decrypt(plain) without key = %q, %v", got, err)
	}
}

func TestFallbackKeyAndReencrypt(t *testing.T) {
	oldKey, newKey := DeriveKey("old"), DeriveKey("new")

	withKeys(t, oldKey)
	sealedOld, _ := Encrypt("secret")

	withKeys(t, newKey, oldKey)
	got, err := Decrypt(sealedOld)
	if err != nil || got != "secret" {
		t.Fatalf("Decrypt with fallback = %q, %v", got, err)
	}
	pt, needed, err := NeedsReencrypt(sealedOld)
	if err != nil || !needed || pt != "secret" {
		t.Fatalf("NeedsReencrypt(old) = %q, %v, %v; want secret, true, nil", pt, needed, err)
	}

	sealedNew, _ := Encrypt("secret")
	if _, needed, err := NeedsReencrypt(sealedNew); err != nil || needed {
		t.Fatalf("NeedsReencrypt(primary) = %v, %v; want false, nil", needed, err)
	}
	if pt, needed, _ := NeedsReencrypt("legacy"); !needed || pt != "legacy" {
		t.Fatalf("NeedsReencrypt(legacy) = %q, %v; want legacy, true", pt, needed)
	}
	if _, needed, err := NeedsReencrypt(""); needed || err != nil {
		t.Fatalf("NeedsReencrypt(\"\") = %v, %v; want false, nil", needed, err)
	}
}

func TestWrongKeyAndTamperedValues(t *testing.T) {
	withKeys(t, DeriveKey("a"))
	sealed, _ := Encrypt("secret")
	// Flip one character inside the payload (not the padding): still
	// well-formed, but fails GCM authentication even with the right key.
	b := []byte(sealed)
	i := len(Prefix) + keyIDLen + 1 + 10
	if b[i] == 'A' {
		b[i] = 'B'
	} else {
		b[i] = 'A'
	}
	tampered := string(b)
	if !IsEncrypted(tampered) {
		t.Fatalf("tampered value %q no longer parses", tampered)
	}
	if _, err := Decrypt(tampered); !errors.Is(err, ErrDecrypt) {
		t.Errorf("Decrypt(tampered) with right key err = %v, want ErrDecrypt", err)
	}

	withKeys(t, DeriveKey("b"))
	for _, v := range []string{sealed, tampered} {
		if _, err := Decrypt(v); !errors.Is(err, ErrDecrypt) {
			t.Errorf("Decrypt(%q) err = %v, want ErrDecrypt", v, err)
		}
		if _, _, err := NeedsReencrypt(v); !errors.Is(err, ErrDecrypt) {
			t.Errorf("NeedsReencrypt(%q) err = %v, want ErrDecrypt", v, err)
		}
	}
}

// Values that merely start with the prefix but are not structurally sealed
// are legacy plaintext: they must round-trip unchanged and be re-encrypted,
// never treated as undecryptable.
func TestPrefixedPlaintextIsLegacy(t *testing.T) {
	withKeys(t, DeriveKey("k"))
	id := PrimaryKeyID()
	for _, v := range []string{
		Prefix,
		Prefix + "AAAA",
		Prefix + "!!!not-base64",
		Prefix + id,           // no payload separator
		Prefix + id + ":!!!",  // bad base64
		Prefix + id + ":AAAA", // too short to be sealed
		Prefix + "nothex!!:" + strings.Repeat("A", 40),
	} {
		if IsEncrypted(v) {
			t.Errorf("IsEncrypted(%q) = true", v)
		}
		if got, err := Decrypt(v); err != nil || got != v {
			t.Errorf("Decrypt(%q) = %q, %v; want unchanged", v, got, err)
		}
		if pt, needed, err := NeedsReencrypt(v); err != nil || !needed || pt != v {
			t.Errorf("NeedsReencrypt(%q) = %q, %v, %v; want plaintext, true, nil", v, pt, needed, err)
		}
	}
}

func TestSealedFormatCarriesKeyID(t *testing.T) {
	withKeys(t, DeriveKey("k1"))
	enc, _ := Encrypt("x")
	if !strings.HasPrefix(enc, PrimarySealedPrefix()) || len(PrimaryKeyID()) != keyIDLen {
		t.Fatalf("sealed value %q lacks primary prefix %q", enc, PrimarySealedPrefix())
	}
	id1 := PrimaryKeyID()
	withKeys(t, DeriveKey("k2"))
	if PrimaryKeyID() == id1 {
		t.Fatal("different keys produced the same key id")
	}
	Reset()
	if PrimaryKeyID() != "" || PrimarySealedPrefix() != "" {
		t.Fatal("key id reported without a keyring")
	}
}

func TestPtrHelpers(t *testing.T) {
	withKeys(t, DeriveKey("k"))

	if p, err := EncryptPtr(nil); p != nil || err != nil {
		t.Fatalf("EncryptPtr(nil) = %v, %v", p, err)
	}
	empty := ""
	if p, err := EncryptPtr(&empty); err != nil || p == nil || *p != "" {
		t.Fatalf("EncryptPtr(\"\") = %v, %v; want pointer to empty", p, err)
	}

	pw := "secret"
	enc, _ := EncryptPtr(&pw)
	DecryptInPlace(&enc)
	if enc == nil || *enc != "secret" {
		t.Fatalf("DecryptInPlace round trip = %v", enc)
	}

	var nilPtr *string
	DecryptInPlace(&nilPtr)
	DecryptInPlace(nil)

	var reported error
	OnDecryptError(func(err error) { reported = err })
	t.Cleanup(func() { OnDecryptError(nil) })
	before := DecryptFailures()
	withKeys(t, DeriveKey("other"))
	bad, _ := Encrypt("sealed-with-another-key")
	withKeys(t, DeriveKey("k"))
	badPtr := &bad
	DecryptInPlace(&badPtr)
	if badPtr != nil {
		t.Fatalf("undecryptable value not cleared: %q", *badPtr)
	}
	if reported == nil || DecryptFailures() != before+1 {
		t.Fatalf("failure not reported: err=%v failures=%d->%d", reported, before, DecryptFailures())
	}
}

func TestSetKeysRejectsBadLength(t *testing.T) {
	t.Cleanup(Reset)
	if err := SetKeys([]byte("short")); err == nil {
		t.Fatal("SetKeys accepted a short key")
	}
	if err := SetKeys(DeriveKey("ok"), []byte("short")); err == nil {
		t.Fatal("SetKeys accepted a short fallback key")
	}
}
