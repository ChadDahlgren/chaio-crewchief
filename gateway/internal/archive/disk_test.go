package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestPutGetRoundTrip(t *testing.T) {
	dir := t.TempDir()
	a, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	blob := []byte("hello world")
	ref, err := a.Put(ctx, blob)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	sum := sha256.Sum256(blob)
	want := hex.EncodeToString(sum[:])
	if ref != want {
		t.Fatalf("ref = %q, want %q", ref, want)
	}
	got, err := a.Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != string(blob) {
		t.Fatalf("Get returned %q, want %q", got, blob)
	}
}

func TestPutIdempotent(t *testing.T) {
	dir := t.TempDir()
	a, _ := New(dir)
	ctx := context.Background()
	blob := []byte("same content")
	ref1, err := a.Put(ctx, blob)
	if err != nil {
		t.Fatalf("Put1: %v", err)
	}
	ref2, err := a.Put(ctx, blob)
	if err != nil {
		t.Fatalf("Put2: %v", err)
	}
	if ref1 != ref2 {
		t.Fatalf("refs differ: %q vs %q", ref1, ref2)
	}
	// ensure only one file exists (idempotent write, no duplication error)
	count := 0
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			count++
		}
		return nil
	})
	if count != 1 {
		t.Fatalf("expected 1 blob file, got %d", count)
	}
}

func TestGetMissingReturnsErrNotExist(t *testing.T) {
	dir := t.TempDir()
	a, _ := New(dir)
	ctx := context.Background()
	_, err := a.Get(ctx, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err == nil {
		t.Fatal("expected error for missing ref")
	}
	if !os.IsNotExist(err) {
		t.Fatalf("expected wrapped os.ErrNotExist, got %v", err)
	}
}

func TestTwoLevelFanOut(t *testing.T) {
	dir := t.TempDir()
	a, _ := New(dir)
	ctx := context.Background()
	blob := []byte("fanout check")
	ref, err := a.Put(ctx, blob)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	want := filepath.Join(dir, ref[:2], ref[2:4], ref)
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected blob at %s, got err: %v", want, err)
	}
}

func TestEmptyBlob(t *testing.T) {
	dir := t.TempDir()
	a, _ := New(dir)
	ctx := context.Background()
	ref, err := a.Put(ctx, []byte{})
	if err != nil {
		t.Fatalf("Put empty: %v", err)
	}
	got, err := a.Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty blob, got %d bytes", len(got))
	}
}
