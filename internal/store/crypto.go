package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// Crypto encrypts secrets (env var values, GitHub tokens) with AES-256-GCM.
type Crypto struct{ aead cipher.AEAD }

func NewCrypto(key []byte) (*Crypto, error) {
	if len(key) != 32 {
		return nil, errors.New("secret key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Crypto{aead: aead}, nil
}

// Seal returns nonce || ciphertext. aad binds the value to its owner
// (e.g. app ID + env name) so ciphertexts cannot be swapped between rows.
func (c *Crypto) Seal(plain []byte, aad string) []byte {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return c.aead.Seal(nonce, nonce, plain, []byte(aad))
}

func (c *Crypto) Open(sealed []byte, aad string) ([]byte, error) {
	n := c.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("ciphertext too short")
	}
	return c.aead.Open(nil, sealed[:n], sealed[n:], []byte(aad))
}

// HashToken is the hex SHA-256 used to store session IDs, API tokens and invite codes.
func HashToken(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
