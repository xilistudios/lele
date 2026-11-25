package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testEnvName = "LELE_TEST_SIGNING_KEY"

type generatedKeyPair struct {
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	publicPEM  []byte
}

func keygen(t *testing.T) generatedKeyPair {
	t.Helper()

	var stdout bytes.Buffer
	if err := run([]string{subcommandKeygen}, &stdout, func(string) string { return "" }); err != nil {
		t.Fatalf("keygen: %v", err)
	}

	values := parseKeygenOutput(t, stdout.String())

	privateKey := decodeBase64(t, values["PRIVATE_KEY_BASE64"])
	if len(privateKey) != ed25519.PrivateKeySize {
		t.Fatalf("private key is %d bytes, want %d", len(privateKey), ed25519.PrivateKeySize)
	}
	publicKey := decodeBase64(t, values["PUBLIC_KEY_BASE64"])
	if len(publicKey) != ed25519.PublicKeySize {
		t.Fatalf("public key is %d bytes, want %d", len(publicKey), ed25519.PublicKeySize)
	}
	publicPEM := decodeBase64(t, values["PUBLIC_KEY_PEM_BASE64"])

	return generatedKeyPair{
		privateKey: ed25519.PrivateKey(privateKey),
		publicKey:  ed25519.PublicKey(publicKey),
		publicPEM:  publicPEM,
	}
}

func parseKeygenOutput(t *testing.T, output string) map[string]string {
	t.Helper()

	values := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("keygen line %q is not KEY=VALUE", line)
		}
		values[key] = value
	}
	return values
}

func decodeBase64(t *testing.T, encoded string) []byte {
	t.Helper()

	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decoding base64: %v", err)
	}
	return raw
}

func envFunc(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func writeChecksums(t *testing.T, dir, content string) string {
	t.Helper()

	path := filepath.Join(dir, "lele_1.2.3_checksums.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing checksums: %v", err)
	}
	return path
}

func TestKeygen_PrintsPEMForTheGeneratedPublicKey(t *testing.T) {
	keys := keygen(t)

	block, rest := pem.Decode(keys.publicPEM)
	if block == nil {
		t.Fatalf("output is not PEM: %q", keys.publicPEM)
	}
	if len(rest) != 0 {
		t.Errorf("PEM has %d trailing bytes", len(rest))
	}
	if block.Type != "PUBLIC KEY" {
		t.Errorf("PEM block type = %q, want PUBLIC KEY", block.Type)
	}

	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatalf("parsing PEM: %v", err)
	}
	pemPublicKey, ok := parsed.(ed25519.PublicKey)
	if !ok {
		t.Fatalf("PEM holds %T, want ed25519.PublicKey", parsed)
	}
	if !bytes.Equal(pemPublicKey, keys.publicKey) {
		t.Error("PEM public key does not match PUBLIC_KEY_BASE64")
	}
}

func TestKeygen_RejectsArguments(t *testing.T) {
	var stdout bytes.Buffer
	err := run([]string{subcommandKeygen, "extra"}, &stdout, func(string) string { return "" })
	if err == nil {
		t.Fatal("expected error for keygen with arguments")
	}
	if stdout.Len() != 0 {
		t.Errorf("keygen wrote %d bytes of output before failing; keys must never leak", stdout.Len())
	}
}

func TestSignVerify_RoundTrip(t *testing.T) {
	keys := keygen(t)
	dir := t.TempDir()
	checksums := writeChecksums(t, dir, "abc123  lele_Linux_x86_64.tar.gz\n")
	signature := filepath.Join(dir, "checksums.txt.sig")

	if err := run(
		[]string{subcommandSign, "--key-env", testEnvName, "--out", signature, checksums},
		&bytes.Buffer{},
		envFunc(map[string]string{testEnvName: base64.StdEncoding.EncodeToString(keys.privateKey)}),
	); err != nil {
		t.Fatalf("sign: %v", err)
	}

	stats, err := os.Stat(signature)
	if err != nil {
		t.Fatalf("stat signature: %v", err)
	}
	if stats.Size() != ed25519.SignatureSize {
		t.Errorf("signature is %d bytes, want %d", stats.Size(), ed25519.SignatureSize)
	}

	var stdout bytes.Buffer
	if err := run(
		[]string{subcommandVerify, "--key-env", testEnvName, checksums, signature},
		&stdout,
		envFunc(map[string]string{testEnvName: base64.StdEncoding.EncodeToString(keys.publicKey)}),
	); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !strings.Contains(stdout.String(), "signature OK") {
		t.Errorf("verify output = %q, want it to mention signature OK", stdout.String())
	}
}

func TestSign_DefaultOutputPath(t *testing.T) {
	keys := keygen(t)
	dir := t.TempDir()
	checksums := writeChecksums(t, dir, "abc123  lele_Linux_x86_64.tar.gz\n")

	if err := run(
		[]string{subcommandSign, "--key-env", testEnvName, checksums},
		&bytes.Buffer{},
		envFunc(map[string]string{testEnvName: base64.StdEncoding.EncodeToString(keys.privateKey)}),
	); err != nil {
		t.Fatalf("sign: %v", err)
	}

	signature, err := os.ReadFile(checksums + signatureSuffix)
	if err != nil {
		t.Fatalf("reading default signature path: %v", err)
	}
	if len(signature) != ed25519.SignatureSize {
		t.Errorf("signature is %d bytes, want %d", len(signature), ed25519.SignatureSize)
	}
}

func TestVerify_TamperedChecksumsFails(t *testing.T) {
	keys := keygen(t)
	dir := t.TempDir()
	checksums := writeChecksums(t, dir, "abc123  lele_Linux_x86_64.tar.gz\n")
	signature := filepath.Join(dir, "checksums.txt.sig")

	keyEnv := envFunc(map[string]string{testEnvName: base64.StdEncoding.EncodeToString(keys.privateKey)})
	if err := run(
		[]string{subcommandSign, "--key-env", testEnvName, "--out", signature, checksums},
		&bytes.Buffer{},
		keyEnv,
	); err != nil {
		t.Fatalf("sign: %v", err)
	}

	if err := os.WriteFile(checksums, []byte("def456  lele_Linux_x86_64.tar.gz\n"), 0o644); err != nil {
		t.Fatalf("tampering checksums: %v", err)
	}

	err := run(
		[]string{subcommandVerify, "--key-env", testEnvName, checksums, signature},
		&bytes.Buffer{},
		envFunc(map[string]string{testEnvName: base64.StdEncoding.EncodeToString(keys.publicKey)}),
	)
	if err == nil {
		t.Fatal("expected verification failure for tampered checksums")
	}
	if !strings.Contains(err.Error(), "invalid signature") {
		t.Errorf("error = %v, want it to mention an invalid signature", err)
	}
}

func TestVerify_WrongKeyFails(t *testing.T) {
	signer := keygen(t)
	other := keygen(t)
	dir := t.TempDir()
	checksums := writeChecksums(t, dir, "abc123  lele_Linux_x86_64.tar.gz\n")
	signature := filepath.Join(dir, "checksums.txt.sig")

	if err := run(
		[]string{subcommandSign, "--key-env", testEnvName, "--out", signature, checksums},
		&bytes.Buffer{},
		envFunc(map[string]string{testEnvName: base64.StdEncoding.EncodeToString(signer.privateKey)}),
	); err != nil {
		t.Fatalf("sign: %v", err)
	}

	err := run(
		[]string{subcommandVerify, "--key-env", testEnvName, checksums, signature},
		&bytes.Buffer{},
		envFunc(map[string]string{testEnvName: base64.StdEncoding.EncodeToString(other.publicKey)}),
	)
	if err == nil {
		t.Fatal("expected verification failure for a signature from a different key")
	}
}

func TestSign_AcceptsSeedAndFullPrivateKey(t *testing.T) {
	keys := keygen(t)
	dir := t.TempDir()
	checksums := writeChecksums(t, dir, "abc123  lele_Linux_x86_64.tar.gz\n")

	var seedEnv bytes.Buffer
	seedSignature := filepath.Join(dir, "seed.sig")
	fullSignature := filepath.Join(dir, "full.sig")

	if err := run(
		[]string{subcommandSign, "--key-env", testEnvName, "--out", seedSignature, checksums},
		&bytes.Buffer{},
		envFunc(map[string]string{
			testEnvName: base64.StdEncoding.EncodeToString(keys.privateKey.Seed()),
		}),
	); err != nil {
		t.Fatalf("sign with seed: %v", err)
	}
	if seedEnv.Len() != 0 {
		t.Errorf("sign wrote to stdout: %q", seedEnv.String())
	}

	if err := run(
		[]string{subcommandSign, "--key-env", testEnvName, "--out", fullSignature, checksums},
		&bytes.Buffer{},
		envFunc(map[string]string{testEnvName: base64.StdEncoding.EncodeToString(keys.privateKey)}),
	); err != nil {
		t.Fatalf("sign with full key: %v", err)
	}

	seedSig, err := os.ReadFile(seedSignature)
	if err != nil {
		t.Fatalf("reading seed signature: %v", err)
	}
	fullSig, err := os.ReadFile(fullSignature)
	if err != nil {
		t.Fatalf("reading full signature: %v", err)
	}
	if !bytes.Equal(seedSig, fullSig) {
		t.Error("seed-derived and full private keys produced different signatures")
	}
}

func TestSign_KeyProblems(t *testing.T) {
	dir := t.TempDir()
	checksums := writeChecksums(t, dir, "abc123  lele_Linux_x86_64.tar.gz\n")

	tests := []struct {
		name      string
		value     string
		wantInErr string
	}{
		{name: "empty", value: "", wantInErr: testEnvName},
		{name: "not base64", value: "not-base64!!", wantInErr: "not valid base64"},
		{name: "too short", value: base64.StdEncoding.EncodeToString([]byte("short")), wantInErr: "expected 32 (seed) or 64"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			signature := filepath.Join(dir, "bad.sig")
			err := run(
				[]string{subcommandSign, "--key-env", testEnvName, "--out", signature, checksums},
				&bytes.Buffer{},
				envFunc(map[string]string{testEnvName: test.value}),
			)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), test.wantInErr) {
				t.Errorf("error = %v, want it to contain %q", err, test.wantInErr)
			}
			if _, statErr := os.Stat(signature); statErr == nil {
				t.Error("signature file was written despite an invalid key")
			}
		})
	}
}

func TestSign_MissingChecksumsFile(t *testing.T) {
	keys := keygen(t)

	err := run(
		[]string{subcommandSign, "--key-env", testEnvName, filepath.Join(t.TempDir(), "absent.txt")},
		&bytes.Buffer{},
		envFunc(map[string]string{testEnvName: base64.StdEncoding.EncodeToString(keys.privateKey)}),
	)
	if err == nil {
		t.Fatal("expected an error for a missing checksums file")
	}
}

func TestVerify_ShortSignatureFails(t *testing.T) {
	keys := keygen(t)
	dir := t.TempDir()
	checksums := writeChecksums(t, dir, "abc123  lele_Linux_x86_64.tar.gz\n")
	signature := filepath.Join(dir, "short.sig")
	if err := os.WriteFile(signature, []byte("too-short"), 0o644); err != nil {
		t.Fatalf("writing short signature: %v", err)
	}

	err := run(
		[]string{subcommandVerify, "--key-env", testEnvName, checksums, signature},
		&bytes.Buffer{},
		envFunc(map[string]string{testEnvName: base64.StdEncoding.EncodeToString(keys.publicKey)}),
	)
	if err == nil {
		t.Fatal("expected an error for a truncated signature")
	}
	if !strings.Contains(err.Error(), "expected 64") {
		t.Errorf("error = %v, want it to mention the expected signature size", err)
	}
}

func TestRun_UsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "no subcommand", args: nil},
		{name: "unknown subcommand", args: []string{"explode"}},
		{name: "sign without file", args: []string{subcommandSign, "--key-env", testEnvName}},
		{name: "sign with two files", args: []string{subcommandSign, "a", "b"}},
		{name: "verify without files", args: []string{subcommandVerify}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := run(test.args, &bytes.Buffer{}, func(string) string { return "" }); err == nil {
				t.Error("expected a usage error")
			}
		})
	}
}

func TestDerivePub_PrintsTheKeyPairPublicKey(t *testing.T) {
	keys := keygen(t)

	for _, test := range []struct {
		name  string
		value []byte
	}{
		{name: "full private key", value: keys.privateKey},
		{name: "seed", value: keys.privateKey.Seed()},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			err := run(
				[]string{subcommandDerivePub, "--key-env", testEnvName},
				&stdout,
				envFunc(map[string]string{testEnvName: base64.StdEncoding.EncodeToString(test.value)}),
			)
			if err != nil {
				t.Fatalf("derive-pub: %v", err)
			}

			output := strings.TrimSpace(stdout.String())
			if strings.ContainsAny(output, " \n") {
				t.Fatalf("derive-pub printed %q, want a single bare base64 public key", output)
			}
			if got, want := output, base64.StdEncoding.EncodeToString(keys.publicKey); got != want {
				t.Errorf("derive-pub = %q, want %q", got, want)
			}
			if strings.Contains(output, base64.StdEncoding.EncodeToString(keys.privateKey)) {
				t.Error("derive-pub printed the private key")
			}
		})
	}
}

func TestDerivePub_KeyProblems(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		wantInErr string
	}{
		{name: "empty", value: "", wantInErr: testEnvName},
		{name: "not base64", value: "not-base64!!", wantInErr: "not valid base64"},
		{name: "too short", value: base64.StdEncoding.EncodeToString([]byte("short")), wantInErr: "expected 32 (seed) or 64"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			err := run(
				[]string{subcommandDerivePub, "--key-env", testEnvName},
				&stdout,
				envFunc(map[string]string{testEnvName: test.value}),
			)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), test.wantInErr) {
				t.Errorf("error = %v, want it to contain %q", err, test.wantInErr)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want nothing printed on a bad key", stdout.String())
			}
		})
	}
}

func TestDerivePub_RejectsArguments(t *testing.T) {
	keys := keygen(t)

	err := run(
		[]string{subcommandDerivePub, "--key-env", testEnvName, "extra"},
		&bytes.Buffer{},
		envFunc(map[string]string{testEnvName: base64.StdEncoding.EncodeToString(keys.privateKey)}),
	)
	if err == nil {
		t.Fatal("expected an error for a stray argument")
	}
	if !strings.Contains(err.Error(), "takes no arguments") {
		t.Errorf("error = %v, want a usage error", err)
	}
}

func TestDerivePub_MatchesThePinnedReleaseKey(t *testing.T) {
	const pinned = "ybtp/SDuO9p/gqHXUBrdORoVDwmDwUiM8NmsxwbPd5o="

	keys := keygen(t)
	var stdout bytes.Buffer
	if err := run(
		[]string{subcommandDerivePub, "--key-env", testEnvName},
		&stdout,
		envFunc(map[string]string{testEnvName: base64.StdEncoding.EncodeToString(keys.privateKey)}),
	); err != nil {
		t.Fatalf("derive-pub: %v", err)
	}
	if strings.TrimSpace(stdout.String()) == pinned {
		t.Fatal("a freshly generated test key derived the production pinned key")
	}
	if decoded, err := base64.StdEncoding.DecodeString(pinned); err != nil || len(decoded) != ed25519.PublicKeySize {
		t.Fatalf("the production pinned key is not a %d byte base64 ed25519 key: %v", ed25519.PublicKeySize, err)
	}
}
