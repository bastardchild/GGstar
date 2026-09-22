package service

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"ggstar/internal/model"
)

func TestValidUsername(t *testing.T) {
	valid := []string{"torvalds", "a", "octo-cat", "user123", "A-B-c", "123", "a1b2c3"}
	for _, u := range valid {
		if !ValidUsername(u) {
			t.Errorf("%q should be valid", u)
		}
	}

	invalid := []string{
		"", "-leading", "trailing-", "has space", "under_score", "dot.dot",
		"slash/slash", "emoji\U0001F600", "user@host", strings.Repeat("a", 40),
		"semi;colon", "<script>", "quote\"", "back`tick", "percent%20",
	}
	for _, u := range invalid {
		if ValidUsername(u) {
			t.Errorf("%q should be rejected", u)
		}
	}
}

func TestBuildTokenURIIsValidBase64JSON(t *testing.T) {
	uri := BuildTokenURIWithBadge(
		"torvalds", "Kernel Whisperer", "Ships kernels.", "C",
		88, 262867, 9, 324427, 42, []string{"C", "Assembly"},
	)

	const prefix = "data:application/json;base64,"
	if !strings.HasPrefix(uri, prefix) {
		t.Fatalf("tokenURI prefix = %q", uri[:min(len(uri), 40)])
	}

	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, prefix))
	if err != nil {
		t.Fatalf("metadata is not valid base64: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("metadata is not valid JSON: %v", err)
	}

	if payload["name"] != "GGstar Skill Badge - torvalds" {
		t.Errorf("unexpected name: %v", payload["name"])
	}
	if payload["title"] != "Kernel Whisperer" {
		t.Errorf("unexpected title: %v", payload["title"])
	}
	if payload["image"] != "/public/avatars/042.svg" {
		t.Errorf("avatar image should be zero-padded and follow the avatarId, got %v", payload["image"])
	}

	attrs, ok := payload["attributes"].([]any)
	if !ok || len(attrs) == 0 {
		t.Fatal("expected attributes array")
	}
	if !strings.Contains(string(raw), "Assembly") {
		t.Error("selected skills must appear in metadata")
	}
}

// TestBuildTokenURIUsesGivenAvatarID locks in the fix for the bug where the
// metadata always advertised avatar 001 regardless of the user's choice.
func TestBuildTokenURIUsesGivenAvatarID(t *testing.T) {
	for _, id := range []int{1, 7, 42, 100} {
		uri := BuildTokenURIWithBadge("u", "t", "s", "Go", 50, 1, 1, 1, id, []string{"Go"})

		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, "data:application/json;base64,"))
		if err != nil {
			t.Fatal(err)
		}

		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}

		want := fmt.Sprintf("/public/avatars/%03d.svg", id)
		if payload["image"] != want {
			t.Errorf("avatarId %d -> image %v, want %v", id, payload["image"], want)
		}
	}
}

// TestBuildTokenURIAttributesIncludeAvatarID keeps the numeric id visible to
// indexers even though the image is a file reference.
func TestBuildTokenURIAttributesIncludeAvatarID(t *testing.T) {
	uri := BuildTokenURIWithBadge("u", "t", "s", "Go", 50, 1, 1, 1, 42, []string{"Go"})

	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, "data:application/json;base64,"))
	if !strings.Contains(string(raw), `"trait_type":"Avatar","value":42`) {
		t.Errorf("expected an Avatar attribute with value 42, got %s", raw)
	}
}

func TestBuildTokenURIFromAnalysisIsUsable(t *testing.T) {
	a := model.Analysis{
		Username: "torvalds", SkillScore: 88, TopSkills: []string{"C"},
		Summary: "s", DominantLang: "C", TotalStars: 10, PublicRepos: 2, Followers: 3,
	}

	uri := BuildTokenURI(a, nil)
	if !strings.HasPrefix(uri, "data:application/json;base64,") {
		t.Fatalf("unexpected tokenURI: %.40q", uri)
	}

	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, "data:application/json;base64,"))
	if err != nil {
		t.Fatalf("metadata is not valid base64: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("metadata is not valid JSON: %v", err)
	}
	if !strings.HasSuffix(payload["image"].(string), ".svg") {
		t.Errorf("image should point at a generated avatar, got %v", payload["image"])
	}
}

func TestBuildTokenURIClampsAvatarID(t *testing.T) {
	uri := BuildTokenURIWithBadge("u", "t", "s", "Go", 50, 1, 1, 1, 999, []string{"Go"})

	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, "data:application/json;base64,"))
	if err != nil {
		t.Fatal(err)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["image"] != "/public/avatars/100.svg" {
		t.Errorf("out-of-range avatar must clamp into 1..100, got %v", payload["image"])
	}
}

func TestBuildTokenURIReflectsOnlyPassedSkills(t *testing.T) {
	uri := BuildTokenURIWithBadge("u", "t", "s", "Go", 50, 1, 1, 1, 1, []string{"Go", "Rust"})

	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, "data:application/json;base64,"))
	body := string(raw)

	if !strings.Contains(body, "Rust") {
		t.Error("expected curated skill to be present")
	}
	if strings.Contains(body, "Solidity") {
		t.Error("metadata must not contain skills the user removed")
	}
}

func TestHTTPStatusFor(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, 200},
		{errString("github user \"x\" not found"), 404},
		{errString("invalid github username"), 400},
		{errString("something exploded"), 502},
	}
	for _, c := range cases {
		if got := HTTPStatusFor(c.err); got != c.want {
			t.Errorf("HTTPStatusFor(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
