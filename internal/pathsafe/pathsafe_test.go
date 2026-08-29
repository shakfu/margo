package pathsafe

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestContainedAcceptsPathsInsideRoot(t *testing.T) {
	root := t.TempDir()
	cases := []string{
		filepath.Join(root, "a.txt"),
		filepath.Join(root, "nested", "deep", "a.txt"),
		root,
		filepath.Join(root, ".", "a.txt"),
		filepath.Join(root, "x", "..", "a.txt"),
	}
	for _, c := range cases {
		if _, err := Contained(root, c); err != nil {
			t.Errorf("Contained(%q) = %v, want nil", c, err)
		}
	}
}

func TestContainedRejectsPathsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	cases := map[string]string{
		"parent directory":  filepath.Dir(root),
		"traversal":         filepath.Join(root, "..", "escape.txt"),
		"deep traversal":    filepath.Join(root, "a", "..", "..", "escape.txt"),
		"unrelated absolue": filepath.Join(t.TempDir(), "other.txt"),
		// The classic prefix bug: "/tmp/rootX" starts with "/tmp/root"
		// but is not inside it. A strings.HasPrefix check passes this.
		"sibling sharing a prefix": root + "-sibling",
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Contained(root, c); !errors.Is(err, ErrEscapesRoot) {
				t.Errorf("Contained(%q) = %v, want ErrEscapesRoot", c, err)
			}
		})
	}
}

// The returned path is the unresolved absolute form: callers show it to
// the user, and on macOS a resolved /var/... becomes /private/var/...
func TestContainedReturnsUnresolvedAbsolutePath(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "doc.qmd")
	got, err := Contained(root, want)
	if err != nil {
		t.Fatalf("Contained: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want the unresolved %q", got, want)
	}
}

func TestContainedResolvesRelativeCandidates(t *testing.T) {
	root := t.TempDir()
	// A relative path resolves against the process working directory,
	// which is not the root, so it must be rejected.
	if _, err := Contained(root, "relative.txt"); !errors.Is(err, ErrEscapesRoot) {
		t.Errorf("a relative path outside root was accepted: %v", err)
	}
}

// A symlink inside the root pointing out of it must not be usable as a
// redirect. This is the case a plain lexical check misses.
func TestContainedRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()

	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if _, err := Contained(root, filepath.Join(link, "payload.txt")); !errors.Is(err, ErrEscapesRoot) {
		t.Errorf("a symlink out of the root was accepted: %v", err)
	}
}

// A symlink that stays inside the root is fine.
func TestContainedAllowsSymlinkWithinRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on Windows")
	}
	root := t.TempDir()
	target := filepath.Join(root, "real")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(root, "alias")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := Contained(root, filepath.Join(link, "a.txt")); err != nil {
		t.Errorf("an in-root symlink was rejected: %v", err)
	}
}

// The leaf usually does not exist yet — the caller is about to create
// it — so resolution has to work from the deepest existing ancestor.
func TestContainedHandlesNonExistentLeaf(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c", "not-created-yet.txt")
	if _, err := Contained(root, deep); err != nil {
		t.Errorf("Contained rejected a not-yet-created path: %v", err)
	}
}

func TestContainedInAny(t *testing.T) {
	r1 := t.TempDir()
	r2 := t.TempDir()
	outside := t.TempDir()

	if _, err := ContainedInAny([]string{r1, r2}, filepath.Join(r2, "a.txt")); err != nil {
		t.Errorf("a path in the second root was rejected: %v", err)
	}
	if _, err := ContainedInAny([]string{r1, r2}, filepath.Join(outside, "a.txt")); !errors.Is(err, ErrEscapesRoot) {
		t.Errorf("a path outside every root was accepted: %v", err)
	}
	// Empty roots are skipped, not treated as "/".
	if _, err := ContainedInAny([]string{"", ""}, "/etc/passwd"); !errors.Is(err, ErrEscapesRoot) {
		t.Errorf("an empty root list accepted an arbitrary path: %v", err)
	}
	if _, err := ContainedInAny(nil, "/etc/passwd"); !errors.Is(err, ErrEscapesRoot) {
		t.Errorf("a nil root list accepted an arbitrary path: %v", err)
	}
}
