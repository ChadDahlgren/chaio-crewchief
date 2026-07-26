Package: `archive`, file path `internal/archive/disk.go`.

Implement a disk-backed, content-addressed blob store satisfying this interface
(paste verbatim from dispatch/internal/types/types.go, do not modify):

```go
package types

type Archiver interface {
	Put(ctx context.Context, blob []byte) (ref string, err error) // content-addressed
	Get(ctx context.Context, ref string) ([]byte, error)
}
```

Required exported API in package `archive`:

```go
package archive

func New(root string) (*Disk, error)

type Disk struct { /* unexported fields */ }

func (d *Disk) Put(ctx context.Context, blob []byte) (string, error)
func (d *Disk) Get(ctx context.Context, ref string) ([]byte, error)
```

`*Disk` must satisfy `types.Archiver` (structurally; you do not need to import types.go
for this file, just match the method signatures exactly, using `context.Context` from
stdlib "context").

Behavior table:

| Method | Behavior |
|---|---|
| New(root) | Creates root dir (MkdirAll, perm 0755) if missing. Returns *Disk, or error if root cannot be created/is not a directory. |
| Put(ctx, blob) | ref = lowercase hex of SHA-256(blob). Stores at `root/ref[0:2]/ref[2:4]/ref` (two-level fan-out, each level a 2-char hex prefix). Creates intermediate dirs as needed. If the file already exists, do NOT error and do NOT rewrite — idempotent no-op, just return the ref. Writes must be atomic-ish: write to a temp file in the same directory then rename, to avoid partial writes on crash. Returns (ref, nil) on success. |
| Get(ctx, ref) | Reads and returns the blob at `root/ref[0:2]/ref[2:4]/ref`. If the file does not exist, return an error that satisfies `os.IsNotExist(err) == true` (wrap the underlying os.ErrNotExist / *PathError — do not swallow it into a generic error string). Any other read error is returned as-is (wrapped with context is fine as long as os.IsNotExist still recognizes the not-exist case via errors.Is). |

Constraints:
- Deps: stdlib only (`context`, `crypto/sha256`, `encoding/hex`, `os`, `path/filepath`, `fmt`, `errors`).
- No package-level global state; everything lives on `*Disk`.
- ctx is accepted for interface compliance; this implementation does no I/O cancellation (fine to ignore ctx body, but keep the parameter).
- ref is always the raw lowercase hex sha256 string (64 chars), not prefixed.

Edge cases you will be tested on:
1. Put then Get round-trips the exact bytes, and ref equals hex(sha256(blob)).
2. Put is idempotent: calling Put twice with identical content does not error and produces the same ref, only one file written on disk.
3. Get on a nonexistent ref returns an error where `os.IsNotExist(err)` is true.
4. The stored file path uses the two-level fan-out: `root/ref[:2]/ref[2:4]/ref`.
5. Put/Get of an empty byte slice works (no crash, round-trips 0 bytes).
