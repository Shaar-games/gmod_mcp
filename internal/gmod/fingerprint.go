package gmod

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

const maxFingerprintSize = 512 << 20

type FileFingerprint struct {
	Path   string `json:"path"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Error  string `json:"error,omitempty"`
}

// FingerprintFile streams a bounded file into SHA-256; it never loads the whole
// asset into memory or includes its contents in tool results.
func FingerprintFile(path string) FileFingerprint {
	result := FileFingerprint{Path: path}
	file, err := os.Open(path)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Size = info.Size()
	if !info.Mode().IsRegular() || info.Size() > maxFingerprintSize {
		result.Error = fmt.Sprintf("file must be regular and at most %d bytes", maxFingerprintSize)
		return result
	}
	hash := sha256.New()
	read, err := io.Copy(hash, io.LimitReader(file, maxFingerprintSize+1))
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if read != info.Size() {
		result.Error = "file size changed while hashing"
		return result
	}
	result.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return result
}
