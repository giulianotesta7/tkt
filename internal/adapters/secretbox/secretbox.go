// Package secretbox seals instance secrets stored in the database — the SMTP
// password (issue #362) and the company sign-in client secrets (issue #419) —
// with AES-256-GCM under a key file that
// lives next to the database, in the data volume (S9).
//
// The key never comes from the environment, so the container contract
// (internal/container) is unchanged: the volume that holds the database
// holds its key. Seal creates the key on first use; Open never does, so a
// database restored without its key file fails with ErrKeyUnavailable and
// root re-enters the secret instead of the instance silently minting a key
// that cannot open anything.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// KeyFileName is the key file the box reads and creates inside its
// directory (the directory of TKT_DB_PATH).
const KeyFileName = "smtp.key"

const (
	keySize = 32
	// sealedPrefix versions the stored format so a later algorithm can be
	// told apart from this one.
	sealedPrefix = "v1:"
)

// additionalData binds every sealed value to this purpose: a value sealed
// for something else under the same key does not open here.
var additionalData = []byte("tkt/secretbox/v1")

var (
	// ErrKeyUnavailable reports a missing, unreadable, or malformed key file.
	ErrKeyUnavailable = errors.New("secretbox: key unavailable")
	// ErrInvalidSealed reports a value that is not a sealed value this key
	// produced: malformed, tampered, or sealed under another key.
	ErrInvalidSealed = errors.New("secretbox: invalid sealed value")
)

// Box seals and opens secrets with the key file in one directory. It is
// safe for concurrent use.
type Box struct {
	path string

	mu  sync.Mutex
	key []byte
}

// New returns a box over the key file in dir. Nothing is read or created
// until the first Seal or Open.
func New(dir string) *Box {
	return &Box{path: filepath.Join(dir, KeyFileName)}
}

// Seal encrypts plaintext and returns its printable sealed form. The first
// Seal creates the key file (owner-only) when it does not exist yet.
func (b *Box) Seal(plaintext []byte) (string, error) {
	return b.seal(plaintext, additionalData)
}

// Open decrypts a value Seal produced. It never creates the key file.
func (b *Box) Open(sealed string) ([]byte, error) {
	return b.open(sealed, additionalData)
}

// For returns a sealer bound to one purpose (issue #419). It shares the key
// file and its cache with the box, but authenticates every value with that
// purpose, so a value sealed for one purpose — or by the box itself, as the
// SMTP password is — never opens as another, and a stored ciphertext cannot
// be moved from one column to another.
func (b *Box) For(purpose string) *Purpose {
	return &Purpose{box: b, additionalData: []byte(string(additionalData) + "/" + purpose)}
}

// Purpose seals and opens the values of one purpose. It is safe for
// concurrent use.
type Purpose struct {
	box            *Box
	additionalData []byte
}

// Seal encrypts plaintext for this purpose, creating the key file on first
// use like Box.Seal.
func (p *Purpose) Seal(plaintext []byte) (string, error) {
	return p.box.seal(plaintext, p.additionalData)
}

// Open decrypts a value this purpose sealed. It never creates the key file.
func (p *Purpose) Open(sealed string) ([]byte, error) {
	return p.box.open(sealed, p.additionalData)
}

func (b *Box) seal(plaintext, additionalData []byte) (string, error) {
	aead, err := b.aead(true)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("secretbox: nonce: %w", err)
	}
	payload := aead.Seal(nonce, nonce, plaintext, additionalData)
	return sealedPrefix + base64.RawURLEncoding.EncodeToString(payload), nil
}

func (b *Box) open(sealed string, additionalData []byte) ([]byte, error) {
	encoded, ok := strings.CutPrefix(sealed, sealedPrefix)
	if !ok {
		return nil, ErrInvalidSealed
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, ErrInvalidSealed
	}
	aead, err := b.aead(false)
	if err != nil {
		return nil, err
	}
	if len(payload) < aead.NonceSize()+aead.Overhead() {
		return nil, ErrInvalidSealed
	}
	nonce, ciphertext := payload[:aead.NonceSize()], payload[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ciphertext, additionalData)
	if err != nil {
		return nil, ErrInvalidSealed
	}
	return plaintext, nil
}

func (b *Box) aead(create bool) (cipher.AEAD, error) {
	key, err := b.loadKey(create)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secretbox: cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

// loadKey returns the cached key, reading the file — or, when create is
// set and the file is absent, creating it — on first use.
func (b *Box) loadKey(create bool) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.key != nil {
		return b.key, nil
	}
	key, err := readKey(b.path)
	if errors.Is(err, os.ErrNotExist) && create {
		key, err = createKey(b.path)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyUnavailable, err)
	}
	b.key = key
	return key, nil
}

func readKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("key file %s has %d bytes, want %d", path, len(key), keySize)
	}
	return key, nil
}

// createKey writes a fresh random key to a temporary file and links it into
// place. The link fails when another process won the race, and then the
// winner's key is read instead, so every caller ends with the same key and
// no caller ever reads a partially written file.
func createKey(path string) ([]byte, error) {
	key := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+KeyFileName+".*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil, err
	}
	if _, err := tmp.Write(key); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Link(tmp.Name(), path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return readKey(path)
		}
		return nil, err
	}
	return key, nil
}
