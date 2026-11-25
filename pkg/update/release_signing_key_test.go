package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func testSigningKeys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating test key pair: %v", err)
	}
	return publicKey, privateKey
}

func TestVerifyChecksumsSignature_Valid(t *testing.T) {
	publicKey, privateKey := testSigningKeys(t)
	checksums := []byte("9f86d0  lele_Linux_x86_64.tar.gz\n")

	if err := VerifyChecksumsSignature(publicKey, checksums, ed25519.Sign(privateKey, checksums)); err != nil {
		t.Fatalf("VerifyChecksumsSignature: %v", err)
	}
}

func TestVerifyChecksumsSignature_TamperedChecksums(t *testing.T) {
	publicKey, privateKey := testSigningKeys(t)
	checksums := []byte("9f86d0  lele_Linux_x86_64.tar.gz\n")
	signature := ed25519.Sign(privateKey, checksums)

	tampered := bytes.Replace(checksums, []byte("9f86d0"), []byte("9f86d1"), 1)

	err := VerifyChecksumsSignature(publicKey, tampered, signature)
	if err == nil {
		t.Fatal("expected an error for tampered checksums")
	}
	if !errors.Is(err, errInvalidReleaseSignature) {
		t.Errorf("error = %v, want errInvalidReleaseSignature", err)
	}
}

func TestVerifyChecksumsSignature_FlippedSignatureByte(t *testing.T) {
	publicKey, privateKey := testSigningKeys(t)
	checksums := []byte("9f86d0  lele_Linux_x86_64.tar.gz\n")
	signature := ed25519.Sign(privateKey, checksums)
	signature[0] ^= 0xff

	err := VerifyChecksumsSignature(publicKey, checksums, signature)
	if err == nil {
		t.Fatal("expected an error for a corrupted signature")
	}
	if !errors.Is(err, errInvalidReleaseSignature) {
		t.Errorf("error = %v, want errInvalidReleaseSignature", err)
	}
}

func TestVerifyChecksumsSignature_WrongKey(t *testing.T) {
	_, privateKey := testSigningKeys(t)
	otherPublicKey, _ := testSigningKeys(t)
	checksums := []byte("9f86d0  lele_Linux_x86_64.tar.gz\n")

	err := VerifyChecksumsSignature(otherPublicKey, checksums, ed25519.Sign(privateKey, checksums))
	if !errors.Is(err, errInvalidReleaseSignature) {
		t.Errorf("error = %v, want errInvalidReleaseSignature", err)
	}
}

func TestVerifyChecksumsSignature_MalformedSignature(t *testing.T) {
	publicKey, privateKey := testSigningKeys(t)
	checksums := []byte("9f86d0  lele_Linux_x86_64.tar.gz\n")

	signature := ed25519.Sign(privateKey, checksums)[:32]

	err := VerifyChecksumsSignature(publicKey, checksums, signature)
	if err == nil {
		t.Fatal("expected an error for a truncated signature")
	}
	if !errors.Is(err, errInvalidReleaseSignature) {
		t.Errorf("error = %v, want errInvalidReleaseSignature", err)
	}
	if !strings.Contains(err.Error(), "expected 64") {
		t.Errorf("error = %v, want it to report the expected signature size", err)
	}
}

func TestVerifyChecksumsSignature_WithoutKey(t *testing.T) {
	_, privateKey := testSigningKeys(t)
	checksums := []byte("9f86d0  lele_Linux_x86_64.tar.gz\n")

	err := VerifyChecksumsSignature(nil, checksums, ed25519.Sign(privateKey, checksums))
	if err == nil {
		t.Fatal("expected an error when no signing key is configured")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("error = %v, want it to report an unconfigured signing key", err)
	}
}

func TestChecksumsSignatureName(t *testing.T) {
	if got, want := ChecksumsSignatureName("v1.2.3"), "lele_1.2.3_checksums.txt.sig"; got != want {
		t.Errorf("ChecksumsSignatureName = %q, want %q", got, want)
	}
	if got, want := ChecksumsSignatureName("1.2.3"), "lele_1.2.3_checksums.txt.sig"; got != want {
		t.Errorf("ChecksumsSignatureName = %q, want %q", got, want)
	}
}

func TestDecodeSigningPublicKey(t *testing.T) {
	publicKey, _ := testSigningKeys(t)
	valid := base64.StdEncoding.EncodeToString(publicKey)

	tests := []struct {
		name    string
		encoded string
		wantNil bool
	}{
		{name: "valid", encoded: valid},
		{name: "valid with padding whitespace", encoded: "  " + valid + "\n"},
		{name: "empty", encoded: "", wantNil: true},
		{name: "not base64", encoded: "not base64!!", wantNil: true},
		{name: "wrong length", encoded: base64.StdEncoding.EncodeToString(publicKey[:16]), wantNil: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := decodeSigningPublicKey(test.encoded)
			if test.wantNil {
				if got != nil {
					t.Fatalf("decodeSigningPublicKey = %x, want nil", got)
				}
				return
			}
			if !bytes.Equal(got, publicKey) {
				t.Errorf("decodeSigningPublicKey = %x, want %x", got, publicKey)
			}
		})
	}
}

func TestPinnedReleaseSigningKeyIsValid(t *testing.T) {
	pinned := ReleaseSigningPublicKey()
	if len(pinned) != ed25519.PublicKeySize {
		t.Fatalf("pinned release signing key is %d bytes, want %d", len(pinned), ed25519.PublicKeySize)
	}
	if bytes.Equal(pinned, make([]byte, ed25519.PublicKeySize)) {
		t.Error("pinned release signing key is all zeroes")
	}

	_, privateKey := testSigningKeys(t)
	checksums := []byte("9f86d0  lele_Linux_x86_64.tar.gz\n")
	if err := VerifyChecksumsSignature(pinned, checksums, ed25519.Sign(privateKey, checksums)); err == nil {
		t.Error("pinned release signing key accepted a signature from an unrelated key")
	}
}
