package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"ggstar/internal/cache"
	"ggstar/internal/config"
	"ggstar/internal/db"
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

// newSavedAnalysisService seeds one stored profile (as Analyze would) and
// returns a Service reading it. MyAnalysis must never touch the network.
func newSavedAnalysisService(t *testing.T) *Service {
	t.Helper()

	store, err := db.Open(filepath.Join(t.TempDir(), "myanalysis.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	a := model.Analysis{
		Username: "torvalds", SkillScore: 88,
		TopSkills:       []string{"C", "Rust"},
		SuggestedTitles: []string{"Kernel Whisperer"},
		Summary:         "Ships kernels.", DominantLang: "C",
		AvatarURL:   "https://avatars.githubusercontent.com/u/1024025",
		PublicRepos: 9, Followers: 100, TotalStars: 500,
	}
	if err := store.UpsertProfile(a, true, "torvalds"); err != nil {
		t.Fatalf("UpsertProfile: %v", err)
	}
	if err := store.SaveTopRepos("torvalds",
		`[{"name":"linux","description":"kernel","language":"C","stars":500,"topics":[]}]`); err != nil {
		t.Fatalf("SaveTopRepos: %v", err)
	}
	if err := store.ReplaceAchievements("torvalds", []db.AchievementRow{
		{Key: "partymember", Name: "Party Member", Tier: "common"},
	}); err != nil {
		t.Fatalf("ReplaceAchievements: %v", err)
	}
	if err := store.ReplaceOrgs("torvalds", []string{"linux"}); err != nil {
		t.Fatalf("ReplaceOrgs: %v", err)
	}

	cacheStore, err := cache.New(context.Background(), cache.Config{Driver: cache.DriverMemory})
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	t.Cleanup(func() { _ = cacheStore.Close() })

	return New(config.Config{}, store, cacheStore)
}

func TestMyAnalysisUnknownLogin(t *testing.T) {
	svc := newSavedAnalysisService(t)

	if _, found, err := svc.MyAnalysis("ghost"); err != nil || found {
		t.Errorf("unknown login: found=%v err=%v, want not found", found, err)
	}
	if _, found, err := svc.MyAnalysis(""); err != nil || found {
		t.Errorf("empty login: found=%v err=%v, want not found", found, err)
	}
}

func TestMyAnalysisRoundTrip(t *testing.T) {
	svc := newSavedAnalysisService(t)

	res, found, err := svc.MyAnalysis("torvalds")
	if err != nil || !found {
		t.Fatalf("MyAnalysis: found=%v err=%v", found, err)
	}
	if res.Analysis.Username != "torvalds" || res.Analysis.SkillScore != 88 {
		t.Errorf("unexpected analysis: %+v", res.Analysis)
	}
	if len(res.Analysis.TopSkills) != 2 || res.Analysis.TopSkills[0] != "C" {
		t.Errorf("top skills not restored: %v", res.Analysis.TopSkills)
	}
	if res.Analysis.Source != "db" || !res.Owned || res.ViewerLogin != "torvalds" {
		t.Errorf("ownership/source not set: %+v", res.Analysis)
	}
	if len(res.Stats.TopRepos) != 1 || res.Stats.TopRepos[0].Name != "linux" {
		t.Errorf("top repos not restored: %+v", res.Stats.TopRepos)
	}
	if res.Stats.MaxRepoStars != 500 || res.Stats.TotalStars != 500 {
		t.Errorf("star stats wrong: %+v", res.Stats)
	}
	if res.Stats.OrgCount != 1 {
		t.Errorf("org count = %d, want 1", res.Stats.OrgCount)
	}
	if res.Stats.UnlockedCount != 1 {
		t.Errorf("unlocked count = %d, want 1", res.Stats.UnlockedCount)
	}
	if len(res.Achievements) == 0 {
		t.Error("expected a non-empty achievements catalogue")
	}
}
