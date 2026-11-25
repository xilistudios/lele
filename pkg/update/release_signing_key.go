package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const (
	ReleaseSigningPublicKeyBase64 = "ybtp/SDuO9p/gqHXUBrdORoVDwmDwUiM8NmsxwbPd5o="

	ReleaseSignatureSuffix = ".sig"
)

var errInvalidReleaseSignature = errors.New("invalid release signature")

var releaseSigningPublicKey = decodeSigningPublicKey(ReleaseSigningPublicKeyBase64)

func ChecksumsSignatureName(version string) string {
	return ChecksumsName(version) + ReleaseSignatureSuffix
}

func ReleaseSigningPublicKey() ed25519.PublicKey {
	return releaseSigningPublicKey
}

func VerifyChecksumsSignature(publicKey ed25519.PublicKey, checksums, signature []byte) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return errors.New("release signing key is not configured")
	}
	if len(signature) != ed25519.SignatureSize {
		return fmt.Errorf(
			"%w: signature is %d bytes, expected %d",
			errInvalidReleaseSignature, len(signature), ed25519.SignatureSize,
		)
	}
	if !ed25519.Verify(publicKey, checksums, signature) {
		return errInvalidReleaseSignature
	}
	return nil
}

func decodeSigningPublicKey(encoded string) ed25519.PublicKey {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil
	}
	return ed25519.PublicKey(raw)
}
