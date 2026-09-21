package badgesvg

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ggstar/internal/avatar"
	"ggstar/internal/model"
)

// TestMain points the avatar package at the generated avatars so the badge can
// inline a real one during tests.
func TestMain(m *testing.M) {
	if st, err := os.Stat(filepath.Join("..", "..", "public", "avatars")); err == nil && st.IsDir() {
		avatar.SetDir(filepath.Join("..", "..", "public", "avatars"))
	}
	os.Exit(m.Run())
}

func sampleBadge() model.Badge {
	return model.Badge{
		Found:          true,
		Address:        "0xAbC1230000000000000000000000000000004567",
		TokenID:        3,
		GithubUsername: "torvalds",
		AvatarID:       42,
		CustomTitle:    "Kernel Whisperer \U0001F451",
		SkillScore:     88,
		Skills:         []string{"C", "Assembly", "Rust"},
		MintedAt:       1735689600,
	}
}

func TestRenderProducesWellFormedXML(t *testing.T) {
	svg := Render(sampleBadge(), "https://scan.bohr.life", "0x1234567890abcdef1234567890abcdef12345678")

	decoder := xml.NewDecoder(strings.NewReader(svg))
	for {
		_, err := decoder.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("SVG is not well-formed XML: %v\n%s", err, svg)
		}
	}
}

func TestRenderClampsAndEscapes(t *testing.T) {
	b := sampleBadge()
	b.SkillScore = 250 // must clamp to 100
	b.CustomTitle = `<script>alert("xss")</script>`
	b.GithubUsername = `evil"<>&`

	svg := Render(b, "https://scan.bohr.life", "0x1234567890abcdef1234567890abcdef12345678")

	if strings.Contains(svg, "<script>") {
		t.Error("SVG must not contain raw <script> from user input")
	}
	if !strings.Contains(svg, "&lt;script&gt;") {
		t.Error("expected escaped script tag")
	}
	if !strings.Contains(svg, "100/100") {
		t.Error("expected clamped score 100/100")
	}
}

func TestRenderEmptyStateIsWellFormed(t *testing.T) {
	svg := Empty()
	if !strings.Contains(svg, "No soulbound badge yet") {
		t.Error("empty state should explain that no badge exists")
	}
	if err := xml.Unmarshal([]byte(svg), new(interface{})); err != nil && !strings.Contains(err.Error(), "cannot unmarshal") {
		t.Fatalf("empty SVG not well-formed: %v", err)
	}
}

func TestSnippetContainsBadgeAndVerifyLinks(t *testing.T) {
	base := "https://ggstar.example"
	explorer := "https://scan.bohr.life"
	contract := "0x1234567890abcdef1234567890abcdef12345678"

	out := Snippet(sampleBadge(), base, explorer, contract)

	wantBadge := base + "/api/badge/0xAbC1230000000000000000000000000000004567.svg"
	if out["badgeUrl"] != wantBadge {
		t.Errorf("badgeUrl = %q, want %q", out["badgeUrl"], wantBadge)
	}
	if !strings.Contains(out["verifyUrl"], contract) || !strings.Contains(out["verifyUrl"], "tokenId=3") {
		t.Errorf("verifyUrl missing contract/tokenId: %q", out["verifyUrl"])
	}
	if !strings.Contains(out["markdown"], wantBadge) || !strings.Contains(out["markdown"], out["verifyUrl"]) {
		t.Error("markdown snippet must include both badge image and verify link")
	}
	if !strings.Contains(out["html"], "img src=") {
		t.Error("html snippet must include an img tag")
	}
	if !strings.Contains(out["twitter"], "@BOTChain_ai") {
		t.Error("tweet must tag @BOTChain_ai")
	}
	if !strings.Contains(out["twitter"], "%23BOTChain") {
		t.Error("tweet hashtags should be URL-encoded")
	}
}

func TestSnippetHandlesMissingContract(t *testing.T) {
	out := Snippet(sampleBadge(), "https://ggstar.example", "https://scan.bohr.life", "")
	if out["verifyUrl"] != "https://scan.bohr.life" {
		t.Errorf("verifyUrl should fall back to explorer root, got %q", out["verifyUrl"])
	}
}

func TestRenderLinksToExplorer(t *testing.T) {
	svg := Render(sampleBadge(), "https://scan.bohr.life", "0x1234567890abcdef1234567890abcdef12345678")
	if !strings.Contains(svg, "https://scan.bohr.life/token/0x1234567890abcdef1234567890abcdef12345678?tokenId=3") {
		t.Error("badge should deep-link to the explorer token page")
	}
	if !strings.Contains(svg, "BOT Chain") {
		t.Error("badge should mention BOT Chain")
	}
}

// TestRenderInlinesAvatar is the important one: browsers refuse to fetch external
// resources inside an SVG loaded via <img>, so the avatar must be inlined.
func TestRenderInlinesAvatar(t *testing.T) {
	svg := Render(sampleBadge(), "https://scan.bohr.life", "0x1234567890abcdef1234567890abcdef12345678")

	if strings.Contains(svg, "<image") || strings.Contains(svg, "href=\"/public/avatars") {
		t.Error("avatar must be inlined, not referenced as an external resource")
	}

	// A nested <svg> element is how the avatar gets positioned inside the badge.
	if strings.Count(svg, "<svg") < 2 {
		t.Fatal("expected a nested avatar <svg> inside the badge")
	}
	if !strings.Contains(svg, `viewBox="0 0 100 100"`) {
		t.Error("nested avatar should keep the DiceBear viewBox")
	}
	if !strings.Contains(svg, "preserveAspectRatio") {
		t.Error("nested avatar needs preserveAspectRatio so it scales predictably")
	}
}

// TestRenderAvatarVariesWithAvatarID proves the on-chain avatarId drives the art.
func TestRenderAvatarVariesWithAvatarID(t *testing.T) {
	base := sampleBadge()

	base.AvatarID = 1
	first := Render(base, "https://scan.bohr.life", "0x1234567890abcdef1234567890abcdef12345678")

	base.AvatarID = 42
	second := Render(base, "https://scan.bohr.life", "0x1234567890abcdef1234567890abcdef12345678")

	if first == second {
		t.Error("badge should change when avatarId changes")
	}
}

// TestRenderStillWorksWithoutAvatarFiles ensures a missing asset degrades
// gracefully instead of producing broken markup.
func TestRenderStillWorksWithoutAvatarFiles(t *testing.T) {
	avatar.SetDir(filepath.Join(t.TempDir(), "missing"))
	t.Cleanup(func() { avatar.SetDir(filepath.Join("..", "..", "public", "avatars")) })

	svg := Render(sampleBadge(), "https://scan.bohr.life", "0x1234567890abcdef1234567890abcdef12345678")

	if !strings.Contains(svg, "</svg>") {
		t.Fatal("badge must still render when the avatar is unavailable")
	}
	if strings.Contains(svg, "<image") {
		t.Error("must not fall back to an external image reference")
	}

	decoder := xml.NewDecoder(strings.NewReader(svg))
	for {
		if _, err := decoder.Token(); err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("badge without avatar is not well-formed: %v", err)
		}
	}
}

func TestNestAvatarKeepsInnerMarkup(t *testing.T) {
	inner := `<svg width="128" height="128" viewBox="0 0 100 100"><circle cx="1" cy="2" r="3"/></svg>`
	nested := nestAvatar(inner, 10, 20, 88)

	if !strings.Contains(nested, `x="10"`) || !strings.Contains(nested, `y="20"`) {
		t.Errorf("nested svg should carry position attributes: %s", nested)
	}
	if !strings.Contains(nested, `width="88"`) || !strings.Contains(nested, `height="88"`) {
		t.Errorf("nested svg should carry the requested size: %s", nested)
	}
	if !strings.Contains(nested, `<circle cx="1" cy="2" r="3"/>`) {
		t.Error("nested avatar content must be preserved")
	}
	if strings.Count(nested, "<svg") != 1 {
		t.Error("the avatar's own root element should be replaced, not duplicated")
	}
}

func TestNestAvatarRejectsGarbage(t *testing.T) {
	if got := nestAvatar("not markup", 0, 0, 10); got != "" {
		t.Errorf("expected empty output for invalid markup, got %q", got)
	}
}

// TestRenderShowsVerificationState makes sure a badge cannot overstate what it
// proves: minting somebody else's username must look different from a real,
// OAuth-backed claim.
func TestRenderShowsVerificationState(t *testing.T) {
	explorer := "https://scan.bohr.life"
	contract := "0x1234567890abcdef1234567890abcdef12345678"

	unverified := sampleBadge()
	unverified.Verified = false
	unverifiedSVG := Render(unverified, explorer, contract)

	verified := sampleBadge()
	verified.Verified = true
	verified.VerifiedLogin = "torvalds"
	verifiedSVG := Render(verified, explorer, contract)

	if !strings.Contains(unverifiedSVG, "Unverified claim") {
		t.Error("an unverified badge must say so explicitly")
	}
	if strings.Contains(unverifiedSVG, "GitHub verified") {
		t.Error("an unverified badge must not claim verification")
	}
	if !strings.Contains(verifiedSVG, "GitHub verified") {
		t.Error("a verified badge should say GitHub verified")
	}
	if strings.Contains(verifiedSVG, "Unverified claim") {
		t.Error("a verified badge must not say unverified")
	}
	if verifiedSVG == unverifiedSVG {
		t.Error("verification state must change the rendered output")
	}
}

func TestVerificationPill(t *testing.T) {
	label, fill, stroke := verificationPill(true)
	if label != "GitHub verified" || fill == "" || stroke == "" {
		t.Errorf("unexpected verified pill: %q %q %q", label, fill, stroke)
	}

	label, fill, stroke = verificationPill(false)
	if label != "Unverified claim" || fill == "" || stroke == "" {
		t.Errorf("unexpected unverified pill: %q %q %q", label, fill, stroke)
	}
}
