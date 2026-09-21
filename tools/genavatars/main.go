// Command genavatars generates the 100 curated Sprouts avatars used by ggstar.
//
// Style: Sprouts by DiceBear, licensed CC0 1.0 (public domain).
// Source: https://www.dicebear.com/styles/sprouts/
//
// The output is deterministic: the same seed always yields byte-identical SVG,
// so regenerating never produces a diff. IDs inside the SVG are derived from the
// seed by DiceBear (idRandomization is disabled), which is what makes it safe to
// inline several of these avatars into a single document.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	dicebear "github.com/dicebear/dicebear-go/v10"
	"github.com/dicebear/styles/v10"
)

const (
	avatarCount = 100
	seedPrefix  = "ggstar"
	fileMode    = 0o644
)

// backgroundColor cycles through the pastel palette used by the ggstar dark
// theme so every avatar sits comfortably on a slate card.
var backgroundColor = []string{
	"#FFB6C1", // pink
	"#E6E6FA", // lavender
	"#98FF98", // mint
	"#B0E0E6", // powder blue
	"#FFDAB9", // peach
	"#DDA0DD", // plum
	"#F0E68C", // khaki
	"#AFEEEE", // pale turquoise
}

var (
	idAttrRe = regexp.MustCompile(`id="([^"]+)"`)
	urlRefRe = regexp.MustCompile(`url\(#([^)]+)\)`)
	hrefRe   = regexp.MustCompile(`href="#([^"]+)"`)
)

// namespaceIDs prefixes every internal SVG id and the references to it.
//
// DiceBear emits some ids that are static per variant (for example the pot
// clipPath `dbspp-taper`), so two avatars using the same pot variant would clash
// if both were inlined into one document. Namespacing per avatar removes that
// class of bug entirely, including in SVG/HTML inline contexts.
func namespaceIDs(svg, prefix string) string {
	svg = idAttrRe.ReplaceAllString(svg, `id="`+prefix+`$1"`)
	svg = urlRefRe.ReplaceAllString(svg, `url(#`+prefix+`$1)`)
	svg = hrefRe.ReplaceAllString(svg, `href="#`+prefix+`$1"`)
	return svg
}

func main() {
	outDir := flag.String("out", filepath.Join("..", "..", "public", "avatars"), "output directory")
	flag.Parse()

	style, err := dicebear.NewStyle([]byte(styles.Sprouts))
	if err != nil {
		fail("build style: %v", err)
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fail("create output dir: %v", err)
	}

	seenSVG := make(map[string]int, avatarCount)
	seenID := make(map[string]string, avatarCount*8)

	written := 0
	var totalBytes, minBytes, maxBytes int
	minBytes = 1 << 30

	for i := 1; i <= avatarCount; i++ {
		seed := fmt.Sprintf("%s-%03d", seedPrefix, i)

		svg, err := render(style, seed, backgroundColor[(i-1)%len(backgroundColor)])
		if err != nil {
			fail("render %s: %v", seed, err)
		}

		svg = namespaceIDs(svg, fmt.Sprintf("gg%03d-", i))

		if prev, dup := seenSVG[svg]; dup {
			fail("avatar %s is identical to avatar %03d", seed, prev)
		}
		seenSVG[svg] = i

		for _, m := range idAttrRe.FindAllStringSubmatch(svg, -1) {
			if owner, dup := seenID[m[1]]; dup {
				fail("duplicate SVG id %q in %s (already used by %s)", m[1], seed, owner)
			}
			seenID[m[1]] = seed
		}

		path := filepath.Join(*outDir, fmt.Sprintf("%03d.svg", i))
		if err := os.WriteFile(path, []byte(svg), fileMode); err != nil {
			fail("write %s: %v", path, err)
		}

		written++
		size := len(svg)
		totalBytes += size
		if size < minBytes {
			minBytes = size
		}
		if size > maxBytes {
			maxBytes = size
		}
	}

	if written != avatarCount {
		fail("expected %d avatars, wrote %d", avatarCount, written)
	}

	// Guard against stale artifacts from a previous generator.
	if extras, err := staleFiles(*outDir); err != nil {
		fail("scan output dir: %v", err)
	} else if len(extras) > 0 {
		fail("unexpected files in %s: %v", *outDir, extras)
	}

	fmt.Printf("generated %d unique Sprouts avatars -> %s\n", written, *outDir)
	fmt.Printf("svg bytes: min=%d avg=%d max=%d total=%d\n",
		minBytes, totalBytes/written, maxBytes, totalBytes)
	fmt.Printf("distinct svg ids: %d\n", len(seenID))
	fmt.Println("license: Sprouts by DiceBear, CC0 1.0 (public domain)")
}

func render(style *dicebear.Style, seed, bg string) (string, error) {
	avatar, err := dicebear.NewAvatar(style, map[string]any{
		"seed": seed,

		// Render size and shape. borderRadius 50 makes the canvas circular so
		// the avatar can be clipped into round <img> elements without extra CSS.
		"size":         128,
		"borderRadius": 50,

		// Determinism: never randomise internal SVG ids. DiceBear derives them
		// from the seed instead, which keeps output stable and collision-free.
		"idRandomization": false,

		// Sprouts is an animated style. Pin it, otherwise the SVG carries CSS
		// animation classes that are meaningless in a static badge.
		"animationVariant": []string{"none"},

		// Keep the avatar on the ggstar dark theme.
		"backgroundColor": []string{bg},
	})
	if err != nil {
		return "", err
	}

	return avatar.SVG(), nil
}

// staleFiles reports files in dir that are not one of the 100 expected avatars.
func staleFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	expected := make(map[string]bool, avatarCount)
	for i := 1; i <= avatarCount; i++ {
		expected[fmt.Sprintf("%03d.svg", i)] = true
	}

	var stale []string
	for _, e := range entries {
		if e.IsDir() || expected[e.Name()] {
			continue
		}
		stale = append(stale, e.Name())
	}
	sort.Strings(stale)
	return stale, nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "genavatars: "+strings.TrimSpace(format)+"\n", args...)
	os.Exit(1)
}
