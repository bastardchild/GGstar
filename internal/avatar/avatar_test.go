package avatar

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain points the package at the real generated avatars when they exist.
// The avatars are produced by tools/genavatars (see the Dockerfile), so a plain
// `go test` run inside the repo finds them at public/avatars.
func TestMain(m *testing.M) {
	candidates := []string{
		filepath.Join("..", "..", "public", "avatars"),
		"public/avatars",
	}

	for _, dir := range candidates {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			SetDir(dir)
			break
		}
	}

	os.Exit(m.Run())
}

func requireAvatars(t *testing.T) {
	t.Helper()

	entries, err := os.ReadDir(Dir())
	if err != nil {
		t.Skipf("avatars not generated yet (%v); run tools/genavatars", err)
	}

	svgs := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".svg") {
			svgs++
		}
	}
	if svgs != Count {
		t.Skipf("expected %d avatars, found %d; run tools/genavatars", Count, svgs)
	}
}

func TestClamp(t *testing.T) {
	cases := map[int]int{
		-100: 1, -1: 1, 0: 1, 1: 1, 42: 42, 100: 100, 101: 100, 99999: 100,
	}
	for in, want := range cases {
		if got := Clamp(in); got != want {
			t.Errorf("Clamp(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestFilenameAndPath(t *testing.T) {
	if got := Filename(7); got != "007.svg" {
		t.Errorf("Filename(7) = %q, want 007.svg", got)
	}
	if got := Filename(100); got != "100.svg" {
		t.Errorf("Filename(100) = %q, want 100.svg", got)
	}
	if got := Filename(1000); got != "100.svg" {
		t.Errorf("Filename out of range should clamp, got %q", got)
	}
	if got := Path(7); got != "/public/avatars/007.svg" {
		t.Errorf("Path(7) = %q", got)
	}
	if got := Path(0); got != "/public/avatars/001.svg" {
		t.Errorf("Path(0) should clamp to 001, got %q", got)
	}
}

func TestAllAvatarsExistAndAreWellFormed(t *testing.T) {
	requireAvatars(t)

	for i := 1; i <= Count; i++ {
		raw, err := os.ReadFile(filepath.Join(Dir(), Filename(i)))
		if err != nil {
			t.Fatalf("avatar %d missing: %v", i, err)
		}

		svg := strings.TrimSpace(string(raw))
		if !strings.HasPrefix(svg, "<svg") {
			t.Errorf("avatar %d does not start with <svg", i)
		}
		if !strings.HasSuffix(svg, "</svg>") {
			t.Errorf("avatar %d does not end with </svg>", i)
		}
		if !strings.Contains(svg, "viewBox=\"0 0 100 100\"") {
			t.Errorf("avatar %d missing expected viewBox", i)
		}

		dec := xml.NewDecoder(strings.NewReader(svg))
		for {
			if _, err := dec.Token(); err != nil {
				if err.Error() == "EOF" {
					break
				}
				t.Fatalf("avatar %d is not well-formed XML: %v", i, err)
			}
		}
	}
}

func TestAllAvatarsAreDistinct(t *testing.T) {
	requireAvatars(t)

	seen := make(map[string]int, Count)
	for i := 1; i <= Count; i++ {
		raw, err := os.ReadFile(filepath.Join(Dir(), Filename(i)))
		if err != nil {
			t.Fatal(err)
		}
		key := string(raw)
		if prev, dup := seen[key]; dup {
			t.Errorf("avatar %03d is identical to avatar %03d", i, prev)
		}
		seen[key] = i
	}
	if len(seen) != Count {
		t.Errorf("expected %d distinct avatars, got %d", Count, len(seen))
	}
}

// TestAvatarIDsDoNotCollide guards the invariant that lets several avatars be
// inlined into one SVG document (the README badge).
func TestAvatarIDsDoNotCollide(t *testing.T) {
	requireAvatars(t)

	owner := map[string]int{}
	for i := 1; i <= Count; i++ {
		raw, err := os.ReadFile(filepath.Join(Dir(), Filename(i)))
		if err != nil {
			t.Fatal(err)
		}

		for _, id := range extractIDs(string(raw)) {
			if prev, dup := owner[id]; dup {
				t.Errorf("svg id %q appears in both avatar %03d and %03d", id, prev, i)
			}
			owner[id] = i
		}
	}
	if len(owner) == 0 {
		t.Error("expected avatars to define internal ids")
	}
}

func extractIDs(svg string) []string {
	var out []string

	for _, part := range strings.Split(svg, `id="`)[1:] {
		if end := strings.Index(part, `"`); end > 0 {
			out = append(out, part[:end])
		}
	}
	return out
}

func TestInlineStripsPrologAndIsDeterministic(t *testing.T) {
	requireAvatars(t)

	first, err := Inline(42)
	if err != nil {
		t.Fatalf("Inline(42): %v", err)
	}
	if strings.HasPrefix(first, "<?xml") {
		t.Error("inline markup must not carry an XML prolog")
	}
	if !strings.HasPrefix(first, "<svg") {
		t.Errorf("inline markup should start with <svg, got %.30q", first)
	}

	second, err := Inline(42)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Error("Inline must be deterministic")
	}
}

func TestInlineClampsOutOfRangeID(t *testing.T) {
	requireAvatars(t)

	out, err := Inline(9999)
	if err != nil {
		t.Fatal(err)
	}
	want, err := Inline(Count)
	if err != nil {
		t.Fatal(err)
	}
	if out != want {
		t.Error("out-of-range id should clamp to the last avatar")
	}
}

func TestInlineUnknownDirReturnsError(t *testing.T) {
	SetDir(filepath.Join(t.TempDir(), "does-not-exist"))

	if _, err := Inline(1); err == nil {
		t.Error("expected an error when the avatar directory is missing")
	}

	// Restore for any later test in the package.
	if st, err := os.Stat(filepath.Join("..", "..", "public", "avatars")); err == nil && st.IsDir() {
		SetDir(filepath.Join("..", "..", "public", "avatars"))
	}
}

func TestStripProlog(t *testing.T) {
	withProlog := `<?xml version="1.0" encoding="UTF-8"?>` + "\n<svg></svg>"
	if got := StripProlog(withProlog); got != "<svg></svg>" {
		t.Errorf("StripProlog = %q", got)
	}
	if got := StripProlog("<svg></svg>"); got != "<svg></svg>" {
		t.Errorf("StripProlog without prolog = %q", got)
	}
	if got := StripProlog("  \n <svg></svg>  "); got != "<svg></svg>" {
		t.Errorf("StripProlog should trim whitespace, got %q", got)
	}
}
