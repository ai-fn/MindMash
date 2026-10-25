package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 64 * 1024
	argonIterations  = 3
	argonParallelism = 4
	argonSaltLen     = 16
	argonKeyLen      = 32
)

// PasswordHashBytes is the memory one HashPassword or VerifyPassword run holds.
const PasswordHashBytes = argonMemory << 10

// HashPassword hashes password using Argon2id and returns an encoded string.
// Format: "$argon2id$v=19$m=<M>,t=<T>,p=<P>$<base64salt>$<base64hash>"
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generate salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLen)

	encoded := fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory,
		argonIterations,
		argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
	return encoded, nil
}

// VerifyPassword returns (false, nil) on mismatch; errors only on parse or crypto failure.
// Uses constant-time comparison to prevent timing attacks.
func VerifyPassword(password, encodedHash string) (bool, error) {
	h, err := parseArgon2Hash(encodedHash)
	if err != nil {
		return false, err
	}

	computed := argon2.IDKey([]byte(password), h.salt, h.iterations, h.memory, h.parallelism, uint32(len(h.hash)))
	if subtle.ConstantTimeCompare(computed, h.hash) == 1 {
		return true, nil
	}
	return false, nil
}

type argon2Hash struct {
	memory, iterations uint32
	parallelism        uint8
	salt, hash         []byte
}

func parseArgon2Hash(encoded string) (argon2Hash, error) {
	parts := strings.Split(encoded, "$")
	// Expected parts after split on "$": ["", "argon2id", "v=19", "m=...,t=...,p=...", "<salt>", "<hash>"]
	if len(parts) != 6 {
		return argon2Hash{}, fmt.Errorf("auth: invalid encoded hash format")
	}
	if parts[1] != "argon2id" {
		return argon2Hash{}, fmt.Errorf("auth: unsupported algorithm %q", parts[1])
	}

	var version int
	if _, scanErr := fmt.Sscanf(parts[2], "v=%d", &version); scanErr != nil {
		return argon2Hash{}, fmt.Errorf("auth: parse version: %w", scanErr)
	}
	if version != argon2.Version {
		return argon2Hash{}, fmt.Errorf("auth: unsupported argon2 version %d", version)
	}

	var m, t, p uint32
	if _, scanErr := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); scanErr != nil {
		return argon2Hash{}, fmt.Errorf("auth: parse params: %w", scanErr)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argon2Hash{}, fmt.Errorf("auth: decode salt: %w", err)
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argon2Hash{}, fmt.Errorf("auth: decode hash: %w", err)
	}

	return argon2Hash{memory: m, iterations: t, parallelism: uint8(p), salt: salt, hash: hash}, nil
}
