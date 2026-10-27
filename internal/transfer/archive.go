// Package transfer implements password-protected, portable instance state files.
package transfer

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"time"
)

const MaxBytes = 128 << 20
const magic = "KUBEPHOS-STATE\x01"

var ErrArchive = errors.New("invalid archive or incorrect password")

type Row = map[string]json.RawMessage

type State struct {
	Format      int               `json:"format"`
	Version     string            `json:"version"`
	CreatedAt   time.Time         `json:"createdAt"`
	Schema      string            `json:"schema"`
	Tables      map[string][]Row  `json:"tables"`
	Credentials map[string][]byte `json:"credentials"`
	Objects     map[string][]byte `json:"objects"`
}

func String(row Row, key string) string {
	var value string
	_ = json.Unmarshal(row[key], &value)
	return value
}
func Set(row Row, key string, value any) { row[key], _ = json.Marshal(value) }

func passwordKey(password string, salt []byte) ([]byte, error) {
	if len(password) < 12 || len(password) > 1024 {
		return nil, errors.New("archive password must contain between 12 and 1024 characters")
	}
	return pbkdf2.Key(sha256.New, password, salt, 600000, 32)
}

type limitedWriter struct {
	out       io.Writer
	remaining int
}

func (w *limitedWriter) Write(value []byte) (int, error) {
	if len(value) > w.remaining {
		return 0, errors.New("state exceeds the 128 MiB archive limit")
	}
	w.remaining -= len(value)
	return w.out.Write(value)
}
func Encrypt(state *State, password string) ([]byte, error) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if err := json.NewEncoder(&limitedWriter{out: writer, remaining: MaxBytes}).Encode(state); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if compressed.Len() > MaxBytes-1024 {
		return nil, errors.New("state exceeds the 128 MiB archive limit")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := passwordKey(password, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	header := append([]byte(magic), salt...)
	header = append(header, nonce...)
	return aead.Seal(header, nonce, compressed.Bytes(), header), nil
}
func Decrypt(value []byte, password string) (*State, error) {
	headerSize := len(magic) + 16 + 12
	if len(value) < headerSize+16 || len(value) > MaxBytes || !bytes.HasPrefix(value, []byte(magic)) {
		return nil, ErrArchive
	}
	header := value[:headerSize]
	key, err := passwordKey(password, header[len(magic):len(magic)+16])
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrArchive
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrArchive
	}
	compressed, err := aead.Open(nil, header[headerSize-12:], value[headerSize:], header)
	if err != nil {
		return nil, ErrArchive
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, ErrArchive
	}
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, MaxBytes+1))
	if err != nil || len(raw) > MaxBytes {
		return nil, ErrArchive
	}
	var state State
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&state) != nil || state.Format != 1 || state.Tables == nil || decoder.Decode(new(any)) != io.EOF {
		return nil, ErrArchive
	}
	return &state, nil
}
