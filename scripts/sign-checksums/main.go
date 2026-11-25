package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	signatureSuffix = ".sig"

	subcommandKeygen    = "keygen"
	subcommandDerivePub = "derive-pub"
	subcommandSign      = "sign"
	subcommandVerify    = "verify"

	signingKeyEnv = "LELE_RELEASE_SIGNING_KEY"

	usage = `usage:
  sign-checksums keygen
  sign-checksums derive-pub [--key-env NAME]
  sign-checksums sign   [--key-env NAME] [--out PATH] CHECKSUMS
  sign-checksums verify [--key-env NAME] CHECKSUMS SIGNATURE`
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Getenv); err != nil {
		fmt.Fprintf(os.Stderr, "sign-checksums: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer, getenv func(string) string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}

	switch args[0] {
	case subcommandKeygen:
		return runKeygen(args[1:], stdout)
	case subcommandDerivePub:
		return runDerivePub(args[1:], stdout, getenv)
	case subcommandSign:
		return runSign(args[1:], getenv)
	case subcommandVerify:
		return runVerify(args[1:], stdout, getenv)
	default:
		return fmt.Errorf("unknown subcommand %q\n%s", args[0], usage)
	}
}

func runKeygen(args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return errors.New("keygen takes no arguments")
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generating ed25519 key pair: %w", err)
	}

	publicPEM, err := publicKeyPEM(publicKey)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(
		stdout,
		"PRIVATE_KEY_BASE64=%s\nPUBLIC_KEY_BASE64=%s\nPUBLIC_KEY_PEM_BASE64=%s\n",
		base64.StdEncoding.EncodeToString(privateKey),
		base64.StdEncoding.EncodeToString(publicKey),
		base64.StdEncoding.EncodeToString(publicPEM),
	)
	if err != nil {
		return fmt.Errorf("writing key pair: %w", err)
	}
	return nil
}

func runDerivePub(args []string, stdout io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet(subcommandDerivePub, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	keyEnv := fs.String("key-env", signingKeyEnv, "environment variable holding the base64 ed25519 private key")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w\n%s", err, usage)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("derive-pub takes no arguments\n%s", usage)
	}

	privateKey, err := loadPrivateKey(getenv(*keyEnv), *keyEnv)
	if err != nil {
		return err
	}

	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return fmt.Errorf("deriving the public key from %s failed", *keyEnv)
	}

	if _, err := fmt.Fprintf(stdout, "%s\n", base64.StdEncoding.EncodeToString(publicKey)); err != nil {
		return fmt.Errorf("writing public key: %w", err)
	}
	return nil
}

func publicKeyPEM(publicKey ed25519.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil, fmt.Errorf("encoding public key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}

func runSign(args []string, getenv func(string) string) error {
	fs := flag.NewFlagSet(subcommandSign, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	keyEnv := fs.String("key-env", signingKeyEnv, "environment variable holding the base64 ed25519 private key")
	out := fs.String("out", "", "signature output path (default: CHECKSUMS.sig)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w\n%s", err, usage)
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("sign expects exactly one checksums file\n%s", usage)
	}

	checksumsPath := fs.Arg(0)
	privateKey, err := loadPrivateKey(getenv(*keyEnv), *keyEnv)
	if err != nil {
		return err
	}

	checksums, err := os.ReadFile(checksumsPath)
	if err != nil {
		return fmt.Errorf("reading checksums: %w", err)
	}

	signaturePath := *out
	if signaturePath == "" {
		signaturePath = checksumsPath + signatureSuffix
	}

	signature := ed25519.Sign(privateKey, checksums)
	if err := os.WriteFile(signaturePath, signature, 0o644); err != nil {
		return fmt.Errorf("writing signature: %w", err)
	}
	return nil
}

func runVerify(args []string, stdout io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet(subcommandVerify, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	keyEnv := fs.String("key-env", signingKeyEnv, "environment variable holding the base64 ed25519 public key")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w\n%s", err, usage)
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("verify expects a checksums file and a signature file\n%s", usage)
	}

	publicKey, err := loadPublicKey(getenv(*keyEnv), *keyEnv)
	if err != nil {
		return err
	}

	checksumsPath, signaturePath := fs.Arg(0), fs.Arg(1)
	checksums, err := os.ReadFile(checksumsPath)
	if err != nil {
		return fmt.Errorf("reading checksums: %w", err)
	}
	signature, err := os.ReadFile(signaturePath)
	if err != nil {
		return fmt.Errorf("reading signature: %w", err)
	}

	if err := verifySignature(publicKey, checksums, signature); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(stdout, "signature OK: %s\n", checksumsPath); err != nil {
		return fmt.Errorf("writing result: %w", err)
	}
	return nil
}

func verifySignature(publicKey ed25519.PublicKey, message, signature []byte) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("public key is %d bytes, expected %d", len(publicKey), ed25519.PublicKeySize)
	}
	if len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("signature is %d bytes, expected %d", len(signature), ed25519.SignatureSize)
	}
	if !ed25519.Verify(publicKey, message, signature) {
		return errors.New("invalid signature")
	}
	return nil
}

func loadPrivateKey(encoded, envName string) (ed25519.PrivateKey, error) {
	raw, err := decodeKey(encoded, envName)
	if err != nil {
		return nil, err
	}

	switch len(raw) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(raw), nil
	default:
		return nil, fmt.Errorf(
			"%s holds %d bytes, expected %d (seed) or %d (private key)",
			envName, len(raw), ed25519.SeedSize, ed25519.PrivateKeySize,
		)
	}
}

func loadPublicKey(encoded, envName string) (ed25519.PublicKey, error) {
	raw, err := decodeKey(encoded, envName)
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%s holds %d bytes, expected %d", envName, len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

func decodeKey(encoded, envName string) ([]byte, error) {
	if encoded == "" {
		return nil, fmt.Errorf("environment variable %s is empty", envName)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("environment variable %s is not valid base64: %w", envName, err)
	}
	return raw, nil
}
