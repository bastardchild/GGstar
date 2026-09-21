// Package avatar resolves the curated Sprouts avatars that ship with ggstar.
//
// The 100 avatars are generated at build time by tools/genavatars (DiceBear
// Sprouts, CC0 1.0) and stored as public/avatars/001.svg ... 100.svg. On-chain
// only the numeric avatarId (1..100) is recorded, so this package is the single
// place that maps that id back to a file.
package avatar

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// Count is the number of curated avatars. The smart contract enforces the
	// same bound (InvalidAvatarId), so the two must stay in sync.
	Count = 100

	// URLPrefix is the public path served by Fiber's static middleware.
	URLPrefix = "/public/avatars"

	defaultDir = "public/avatars"
)

var (
	dirOnce   sync.Once
	dirPath   string
	inlineMu  sync.RWMutex
	inlineMem = map[int]string{}
)

// Dir returns the directory holding the generated avatars. AVATAR_DIR overrides
// the default so tests and non-standard deployments can point elsewhere.
func Dir() string {
	dirOnce.Do(func() {
		dirPath = defaultDir
		if custom := strings.TrimSpace(os.Getenv("AVATAR_DIR")); custom != "" {
			dirPath = custom
		}
	})
	return dirPath
}

// SetDir overrides the avatar directory. Intended for tests.
func SetDir(dir string) {
	dirOnce.Do(func() {})
	dirPath = dir
}

// Clamp forces an arbitrary id into the valid 1..Count range.
func Clamp(id int) int {
	if id < 1 {
		return 1
	}
	if id > Count {
		return Count
	}
	return id
}

// Filename returns the file name for an id, e.g. "007.svg".
func Filename(id int) string {
	return fmt.Sprintf("%03d.svg", Clamp(id))
}

// Path returns the public URL for an id, e.g. "/public/avatars/007.svg".
func Path(id int) string {
	return URLPrefix + "/" + Filename(id)
}

// Inline returns the avatar markup with the XML prolog stripped, ready to be
// embedded inside another SVG.
//
// Embedding must be inline rather than via <image href="...">: browsers refuse to
// load external resources inside an SVG that is itself loaded through <img>, and
// that is exactly how a GitHub README renders the badge.
func Inline(id int) (string, error) {
	id = Clamp(id)

	inlineMu.RLock()
	cached, ok := inlineMem[id]
	inlineMu.RUnlock()
	if ok {
		return cached, nil
	}

	raw, err := os.ReadFile(filepath.Join(Dir(), Filename(id)))
	if err != nil {
		return "", fmt.Errorf("read avatar %d: %w", id, err)
	}

	markup := StripProlog(string(raw))

	inlineMu.Lock()
	inlineMem[id] = markup
	inlineMu.Unlock()

	return markup, nil
}

// StripProlog removes an optional XML declaration so the markup can be nested.
func StripProlog(svg string) string {
	s := strings.TrimSpace(svg)
	if strings.HasPrefix(s, "<?xml") {
		if end := strings.Index(s, "?>"); end >= 0 {
			s = strings.TrimSpace(s[end+2:])
		}
	}
	return s
}
