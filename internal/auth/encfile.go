package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// userHomeDir resolves the home directory for ~/.tallyfy. A var so tests can
// redirect the encrypted-file backend into a temp dir (tests must never
// touch the real ~/.tallyfy).
var userHomeDir = os.UserHomeDir

const (
	credFileName = "credentials.enc"
	keyFileName  = ".credkey"
	keySize      = 32 // AES-256
)

// credFile is the JSON plaintext sealed inside credentials.enc.
type credFile struct {
	Token string `json:"token"`
}

// tallyfyDir returns ~/.tallyfy (not created).
func tallyfyDir() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".tallyfy"), nil
}

// encSave encrypts {"token":...} with AES-256-GCM under the key in
// ~/.tallyfy/.credkey (created on first use, 0600 in a 0700 dir) and writes
// base64(nonce||ciphertext) atomically to ~/.tallyfy/credentials.enc (0600).
func encSave(token string) error {
	dir, err := tallyfyDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	key, err := loadOrCreateKey(dir)
	if err != nil {
		return err
	}

	plaintext, err := json.Marshal(credFile{Token: token})
	if err != nil {
		return fmt.Errorf("encoding credentials: %w", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize()) // 12 bytes for standard GCM
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("generating nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, plaintext, nil)

	payload := base64.StdEncoding.EncodeToString(append(nonce, sealed...))
	credPath := filepath.Join(dir, credFileName)
	if err := writeFileAtomic(credPath, []byte(payload), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", credPath, err)
	}
	return nil
}

// encLoad decrypts credentials.enc. A missing credentials file means "no
// stored token": ("", nil), with no side effects on disk.
func encLoad() (string, error) {
	dir, err := tallyfyDir()
	if err != nil {
		return "", err
	}
	credPath := filepath.Join(dir, credFileName)
	b64, err := os.ReadFile(credPath)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", credPath, err)
	}

	keyPath := filepath.Join(dir, keyFileName)
	key, err := os.ReadFile(keyPath)
	if errors.Is(err, fs.ErrNotExist) {
		return "", corruptCredentials(credPath, errors.New("its key file is missing"))
	}
	if err != nil {
		return "", fmt.Errorf("reading credential key %s: %w", keyPath, err)
	}
	if len(key) != keySize {
		return "", corruptCredentials(credPath, fmt.Errorf("key file is %d bytes, want %d", len(key), keySize))
	}

	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b64)))
	if err != nil {
		return "", corruptCredentials(credPath, err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	if len(raw) <= gcm.NonceSize() {
		return "", corruptCredentials(credPath, errors.New("file too short"))
	}
	plaintext, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", corruptCredentials(credPath, err)
	}
	var cf credFile
	if err := json.Unmarshal(plaintext, &cf); err != nil {
		return "", corruptCredentials(credPath, err)
	}
	return cf.Token, nil
}

// encDelete removes credentials.enc; a missing file is success. The key
// file is kept: it holds no secret material derived from the token and is
// reused by the next `tallyfy login`.
func encDelete() error {
	dir, err := tallyfyDir()
	if err != nil {
		return err
	}
	credPath := filepath.Join(dir, credFileName)
	if err := os.Remove(credPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", credPath, err)
	}
	return nil
}

// loadOrCreateKey reads ~/.tallyfy/.credkey or creates it with 32 bytes
// from crypto/rand, mode 0600.
func loadOrCreateKey(dir string) ([]byte, error) {
	path := filepath.Join(dir, keyFileName)
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(b) != keySize {
			return nil, fmt.Errorf("credential key file %s is corrupted (%d bytes, want %d); delete it together with %s and run `tallyfy login` again",
				path, len(b), keySize, filepath.Join(dir, credFileName))
		}
		return b, nil
	case errors.Is(err, fs.ErrNotExist):
		key := make([]byte, keySize)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, fmt.Errorf("generating credential key: %w", err)
		}
		if err := writeFileAtomic(path, key, 0o600); err != nil {
			return nil, fmt.Errorf("writing credential key %s: %w", path, err)
		}
		return key, nil
	default:
		return nil, fmt.Errorf("reading credential key %s: %w", path, err)
	}
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initializing cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initializing AES-GCM: %w", err)
	}
	return gcm, nil
}

// corruptCredentials builds the user-facing "stored credentials unreadable"
// error with a recovery hint.
func corruptCredentials(path string, cause error) error {
	return fmt.Errorf("stored credentials at %s cannot be decrypted (%v); delete the file and run `tallyfy login` again", path, cause)
}

// writeFileAtomic writes data via a same-directory temp file + rename so
// readers never observe a partial file, with perm applied before content.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
