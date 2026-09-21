package ai

import (
	"strings"
	"testing"
)

func baseInput() Input {
	return Input{
		Username:     "torvalds",
		Name:         "Linus Torvalds",
		PublicRepos:  9,
		Followers:    324427,
		AccountYears: 14,
		TotalStars:   262867,
		MaxRepoStars: 180000,
		DominantLang: "C",
		Languages:    []string{"C", "Assembly", "Rust", "Shell"},
	}
}

func TestStripFencesHandlesMarkdownBlocks(t *testing.T) {
	cases := map[string]string{
		"```json\n{\"a\":1}\n```":     "{\"a\":1}",
		"```\n{\"a\":1}\n```":         "{\"a\":1}",
		"Here you go:\n{\"a\":1}":     "{\"a\":1}",
		"{\"a\":1}":                   "{\"a\":1}",
		"  ```json {\"a\":1} ```  ":   "{\"a\":1}",
		"Sure!\n```json\n{\"a\":1}":   "{\"a\":1}",
	}

	for in, want := range cases {
		if got := StripFences(in); got != want {
			t.Errorf("StripFences(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeClampsScore(t *testing.T) {
	in := baseInput()

	for _, tc := range []struct{ in, want int }{
		{0, 1}, {-50, 1}, {1, 1}, {100, 100}, {250, 100},
	} {
		got := Normalize(Result{SkillScore: tc.in}, in)
		if got.SkillScore != tc.want {
			t.Errorf("SkillScore %d -> %d, want %d", tc.in, got.SkillScore, tc.want)
		}
	}
}

func TestNormalizeDedupesSkillsAndCapsAtSix(t *testing.T) {
	in := baseInput()
	res := Result{
		SkillScore: 70,
		TopSkills: []string{
			"Go", "go", " Go ", "Solidity", "Rust", "TypeScript", "Python", "C", "SQL",
		},
	}

	got := Normalize(res, in)

	if len(got.TopSkills) != 6 {
		t.Fatalf("expected 6 skills, got %d (%v)", len(got.TopSkills), got.TopSkills)
	}
	seen := map[string]bool{}
	for _, s := range got.TopSkills {
		key := strings.ToLower(s)
		if seen[key] {
			t.Errorf("duplicate skill %q", s)
		}
		seen[key] = true
	}
}

func TestNormalizeFillsMissingSkillsAndTitles(t *testing.T) {
	in := baseInput()

	got := Normalize(Result{SkillScore: 50}, in)

	if len(got.TopSkills) == 0 {
		t.Error("expected fallback skills from input languages")
	}
	if len(got.SuggestedTitles) != 4 {
		t.Errorf("expected exactly 4 titles, got %d (%v)", len(got.SuggestedTitles), got.SuggestedTitles)
	}
	if got.Summary == "" {
		t.Error("expected fallback summary")
	}
}

func TestMockIsDeterministicAndBounded(t *testing.T) {
	in := baseInput()

	first := Mock(in)
	second := Mock(in)

	if first.SkillScore != second.SkillScore {
		t.Error("Mock must be deterministic for the same input")
	}
	if first.SkillScore < 1 || first.SkillScore > 100 {
		t.Errorf("score out of range: %d", first.SkillScore)
	}
	if len(first.SuggestedTitles) != 4 {
		t.Errorf("expected 4 titles, got %d", len(first.SuggestedTitles))
	}
	if !strings.Contains(first.Summary, "torvalds") && !strings.Contains(first.Summary, "Linus") {
		t.Errorf("summary should mention the profile: %q", first.Summary)
	}
}

func TestMockScoresHigherForStrongerProfiles(t *testing.T) {
	weak := Input{Username: "newbie", PublicRepos: 1, Followers: 0, Languages: []string{"HTML"}}
	strong := baseInput()

	if Mock(strong).SkillScore <= Mock(weak).SkillScore {
		t.Errorf("stronger profile should score higher: %d vs %d",
			Mock(strong).SkillScore, Mock(weak).SkillScore)
	}
}

func TestNormalizeTruncatesLongSummary(t *testing.T) {
	in := baseInput()
	got := Normalize(Result{SkillScore: 60, Summary: strings.Repeat("x", 900)}, in)
	if len(got.Summary) > 400 {
		t.Errorf("summary should be truncated, got %d chars", len(got.Summary))
	}
}

func TestAnalyzeWithoutKeyUsesMock(t *testing.T) {
	c := New("https://api.openai.com/v1", "", "gpt-4o-mini", 0, 0)
	if c.Enabled() {
		t.Fatal("client with empty key must report not enabled")
	}

	res, source := c.Analyze(t.Context(), baseInput())
	if source != "mock" {
		t.Errorf("source = %q, want mock", source)
	}
	if res.SkillScore < 1 {
		t.Error("mock result must be normalized")
	}
}

func TestAnalyzeFallsBackOnUnreachableProvider(t *testing.T) {
	// Port 0 is never routable, so the HTTP call must fail and degrade to mock.
	c := New("http://127.0.0.1:1/v1", "sk-test", "gpt-4o-mini", 1, 100)

	res, source := c.Analyze(t.Context(), baseInput())
	if source != "mock" {
		t.Errorf("source = %q, want mock fallback", source)
	}
	if res.SkillScore < 1 || res.SkillScore > 100 {
		t.Errorf("fallback score out of range: %d", res.SkillScore)
	}
}

func TestToAnalysisCarriesStats(t *testing.T) {
	in := baseInput()
	a := ToAnalysis(in, Mock(in), "mock")

	if a.Username != in.Username || a.TotalStars != in.TotalStars {
		t.Errorf("stats not carried over: %+v", a)
	}
	if a.Source != "mock" || a.UpdatedAt == "" {
		t.Errorf("source/timestamp missing: %+v", a)
	}
}
