package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestVerifySum(t *testing.T) {
	data := []byte("the binary")
	h := sha256.Sum256(data)
	sums := hex.EncodeToString(h[:]) + "  hadi-linux-amd64\nother  hadi-darwin-arm64\n"

	if err := verifySum(sums, "hadi-linux-amd64", data); err != nil {
		t.Fatalf("valid sum rejected: %v", err)
	}
	if err := verifySum(sums, "hadi-linux-amd64", []byte("tampered")); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("tampered binary accepted: %v", err)
	}
	if err := verifySum(sums, "hadi-windows-amd64", data); err == nil || !strings.Contains(err.Error(), "not listed") {
		t.Fatalf("unlisted asset accepted: %v", err)
	}
}

func TestAssetURL(t *testing.T) {
	rel := &ghRelease{}
	rel.Assets = []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	}{
		{Name: "hadi-linux-amd64", URL: "https://example.com/a"},
		{Name: "sha256sums.txt", URL: "https://example.com/s"},
	}
	if assetURL(rel, "hadi-linux-amd64") != "https://example.com/a" {
		t.Error("exact match failed")
	}
	if assetURL(rel, "hadi-linux-arm64") != "" {
		t.Error("missing asset should return empty")
	}
}
