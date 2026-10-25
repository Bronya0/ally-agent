// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func updateFileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type updateArchiveReceipt struct {
	Tag    string `json:"tag"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func updateHTTPClient(cfg ConfigState, timeout time.Duration) *http.Client {
	client := proxyHTTPClient(cfg, false, timeout)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return errors.New("update redirect must use HTTPS")
		}
		if len(via) >= 10 {
			return errors.New("too many update redirects")
		}
		return nil
	}
	return client
}

// The receipt detects truncation and changes between download and installation.
// It is local integrity evidence, not an independent publisher signature.
func saveUpdateReceipt(path, tag, name string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("archive is not a regular file")
	}
	digest, err := updateFileDigest(path)
	if err != nil {
		return err
	}
	data, err := json.Marshal(updateArchiveReceipt{Tag: tag, Name: name, Size: info.Size(), SHA256: digest})
	if err != nil {
		return err
	}
	return writeAtomicBytes(path+".receipt.json", data, 0o600)
}

func verifyUpdateReceipt(path, tag, name string) error {
	file, err := os.Open(path + ".receipt.json")
	if err != nil {
		return err
	}
	defer file.Close()
	var receipt updateArchiveReceipt
	if err := json.NewDecoder(io.LimitReader(file, 4096)).Decode(&receipt); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || receipt.Tag != tag || receipt.Name != name || receipt.Size != info.Size() || receipt.Size <= 0 || receipt.Size > maxUpdateArchiveBytes {
		return errors.New("update archive receipt mismatch; download the update again")
	}
	digest, err := updateFileDigest(path)
	if err != nil {
		return err
	}
	if digest != receipt.SHA256 {
		return fmt.Errorf("update archive changed after download; download %s again", tag)
	}
	return nil
}

func verifyStagedUpdate(tag string) error {
	return verifyUpdateReceipt(updateAssetPath(tag), tag, expectedAssetName(tag))
}
