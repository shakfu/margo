// Package pathsafe confines filesystem paths supplied by untrusted
// callers — a model's tool arguments, a front-end's IPC payload — to a
// directory the caller is allowed to touch.
//
// The check exists because "the model asked for this path" is not
// evidence the user wanted it written, read, or opened. Callers resolve
// once, up front, and use the returned path from then on.
package pathsafe

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrEscapesRoot is returned when a candidate path resolves outside the
// root it was checked against. Callers match with errors.Is.
var ErrEscapesRoot = errors.New("path escapes the allowed directory")

// Contained returns candidate as an absolute path, but only when it stays
// inside root. Both sides are symlink-resolved before comparison so a
// symlink cannot redirect the result outside root.
//
// The returned path is the *unresolved* absolute form: it is what the
// caller shows the user, and operating through it follows the same
// symlinks to the same already-verified location.
func Contained(root, candidate string) (string, error) {
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	rel, err := filepath.Rel(resolveDeepest(absRoot), resolveDeepest(abs))
	if err != nil {
		return "", ErrEscapesRoot
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrEscapesRoot
	}
	return abs, nil
}

// ContainedInAny returns the first root that contains candidate. Used
// where several directories are legitimate (margo's output directory and
// its attachment store, say) and any one of them is enough.
func ContainedInAny(roots []string, candidate string) (string, error) {
	for _, r := range roots {
		if r == "" {
			continue
		}
		if abs, err := Contained(r, candidate); err == nil {
			return abs, nil
		}
	}
	return "", ErrEscapesRoot
}

// resolveDeepest expands symlinks in the deepest existing ancestor of p
// and re-appends the not-yet-existing tail. filepath.EvalSymlinks fails
// outright on a missing leaf, which is the common case here — the file
// a caller wants to create does not exist yet.
func resolveDeepest(p string) string {
	probe := p
	for {
		if resolved, err := filepath.EvalSymlinks(probe); err == nil {
			return filepath.Join(resolved, strings.TrimPrefix(p, probe))
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return p
		}
		probe = parent
	}
}
