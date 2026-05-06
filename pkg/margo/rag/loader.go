package rag

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultIndexExtensions is the conservative ship-in allowlist of file
// types the loader will read. Plain text formats only — no binary
// (PDF, docx) until a dedicated extractor lands per TODO §7.5.
//
// Extensions are matched case-insensitively and must include the dot.
var DefaultIndexExtensions = []string{".md", ".txt", ".qmd"}

// MaxFileBytes caps any single file the loader will read into memory.
// Larger files are skipped with a warning rather than blowing up RAM
// on an accidental binary or 500 MB log file. Tunable per-call via
// LoadOptions.
const DefaultMaxFileBytes = 1 << 20 // 1 MiB

// LoadedDocument is one file the loader read off disk, ready for
// chunking + embedding.
type LoadedDocument struct {
	// AbsPath is the full filesystem path. Used for change detection
	// (mtime) and for opening the source from agent step events.
	AbsPath string
	// RelPath is AbsPath minus the load root. Stable id-friendly key
	// used downstream as the chunk's `DocPath`.
	RelPath string
	// Content is the file's bytes interpreted as UTF-8.
	Content string
	// ModTime is the file's mtime; lets the indexer skip unchanged
	// files on reindex (slated for 7.1.d.4).
	ModTime time.Time
	// Bytes is the on-disk size in bytes.
	Bytes int64
}

// LoadOptions tunes a Load call. Zero values mean "use defaults."
type LoadOptions struct {
	// Extensions is the allowlist (with leading dots). Empty = use
	// DefaultIndexExtensions.
	Extensions []string
	// MaxFileBytes caps the size of a single file. 0 = use
	// DefaultMaxFileBytes. Files larger than this are skipped.
	MaxFileBytes int64
	// SkipHidden, when true, prunes any directory or file whose name
	// starts with "." (e.g. .git, .DS_Store). Defaults to true; set
	// SkipHidden = false explicitly to include them.
	IncludeHidden bool
}

// Load walks `root` and returns every file whose extension is in the
// allowlist. Files are returned sorted by RelPath for deterministic
// output (tests, diffs of "what's indexed").
//
// Errors on individual files (permission denied, decode failures) are
// non-fatal: the file is skipped and walking continues. A single
// fatal error from filepath.WalkDir on the root is returned.
func Load(ctx context.Context, root string, opts LoadOptions) ([]LoadedDocument, error) {
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("load: stat %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("load: %q is not a directory", root)
	}

	exts := opts.Extensions
	if len(exts) == 0 {
		exts = DefaultIndexExtensions
	}
	extSet := make(map[string]struct{}, len(exts))
	for _, e := range exts {
		extSet[strings.ToLower(e)] = struct{}{}
	}

	maxBytes := opts.MaxFileBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxFileBytes
	}

	var docs []LoadedDocument
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Permission denied on a subdir is non-fatal: skip it,
			// continue walking siblings.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// Honour cancellation between entries — large trees can take
		// noticeable time.
		if ctx.Err() != nil {
			return ctx.Err()
		}

		name := d.Name()
		if !opts.IncludeHidden && strings.HasPrefix(name, ".") && path != root {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		if _, ok := extSet[ext]; !ok {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil // skip; non-fatal
		}
		if fi.Size() > maxBytes {
			return nil // skip oversized
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil // skip unreadable
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		docs = append(docs, LoadedDocument{
			AbsPath: path,
			RelPath: filepath.ToSlash(rel),
			Content: string(content),
			ModTime: fi.ModTime(),
			Bytes:   fi.Size(),
		})
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("load: walk %q: %w", root, walkErr)
	}

	sort.Slice(docs, func(i, j int) bool { return docs[i].RelPath < docs[j].RelPath })
	return docs, nil
}
