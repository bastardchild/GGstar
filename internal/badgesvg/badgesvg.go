package badgesvg

import (
	"fmt"
	"html"
	"strings"

	"ggstar/internal/avatar"
	"ggstar/internal/model"
)

type Theme struct {
	Bg     string
	Card   string
	Pink   string
	Mint   string
	Lav    string
	Text   string
	Muted  string
}

var KawaiiTheme = Theme{
	Bg:    "#0F172A",
	Card:  "#1E293B",
	Pink:  "#FFB6C1",
	Mint:  "#98FF98",
	Lav:   "#E6E6FA",
	Text:  "#F8FAFC",
	Muted: "#94A3B8",
}

func esc(s string) string { return html.EscapeString(s) }

func clampScore(n int) int {
	if n < 0 {
		return 0
	}
	if n > 100 {
		return 100
	}
	return n
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return strings.TrimSpace(s[:max]) + "..."
}

// Badge geometry. The text column sits on the left, the avatar on the right.
const (
	badgeW = 480
	badgeH = 150

	avatarSize = 88
	avatarCX   = badgeW - 22 - avatarSize/2
	avatarCY   = badgeH / 2

	textX     = 26
	barWidth  = 272
	barHeight = 10
)

// nestAvatar turns a standalone avatar SVG into one positioned inside the badge.
//
// The avatar has to be inlined rather than referenced with <image href="...">:
// browsers refuse to load external resources inside an SVG that is itself loaded
// through <img>, which is exactly how GitHub renders a README badge.
func nestAvatar(markup string, x, y, size int) string {
	openEnd := strings.Index(markup, ">")
	if openEnd < 0 {
		return ""
	}
	return fmt.Sprintf(
		`<svg x="%d" y="%d" width="%d" height="%d" viewBox="0 0 100 100" preserveAspectRatio="xMidYMid meet">%s`,
		x, y, size, size, markup[openEnd+1:],
	)
}

// verificationPill renders the trust indicator. The wording is deliberately
// blunt: a badge that was minted for a username its owner never proved must be
// visibly distinguishable, otherwise the badge overstates what it proves.
func verificationPill(verified bool) (label, fill, stroke string) {
	if verified {
		return "GitHub verified", "#064e3b", KawaiiTheme.Mint
	}
	return "Unverified claim", "#4c1d24", "#fca5a5"
}

// Render builds the README-embeddable SVG badge for a minted SBT.
func Render(b model.Badge, explorerURL, contractAddress string) string {
	if !b.Found {
		return Empty()
	}

	const w, h = badgeW, badgeH
	score := clampScore(b.SkillScore)
	barW := float64(score) / 100.0 * float64(barWidth)

	title := truncate(b.CustomTitle, 34)
	if title == "" {
		title = "GGstar Badge Holder"
	}
	skills := truncate(strings.Join(b.Skills, " · "), 42)
	if skills == "" {
		skills = "reputation verified on-chain"
	}

	link := fmt.Sprintf("%s/token/%s?tokenId=%d", strings.TrimRight(explorerURL, "/"), contractAddress, b.TokenID)
	if contractAddress == "" {
		link = explorerURL
	}

	pillLabel, pillFill, pillStroke := verificationPill(b.Verified)
	pillWidth := 8*len(pillLabel) + 26

	// A missing or unreadable avatar file must never break the badge.
	avatarMarkup := ""
	if inline, err := avatar.Inline(int(b.AvatarID)); err == nil {
		avatarMarkup = nestAvatar(inline, avatarCX-avatarSize/2, avatarCY-avatarSize/2, avatarSize)
	}

	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="GGstar Skill Badge for %s">
  <defs>
    <linearGradient id="gg" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0%%" stop-color="%s"/>
      <stop offset="50%%" stop-color="%s"/>
      <stop offset="100%%" stop-color="%s"/>
    </linearGradient>
    <clipPath id="round"><rect x="1" y="1" width="%d" height="%d" rx="16"/></clipPath>
  </defs>
  <g clip-path="url(#round)">
    <rect x="1" y="1" width="%d" height="%d" rx="16" fill="%s"/>
    <rect x="1" y="1" width="8" height="%d" fill="url(#gg)"/>
  </g>
  <rect x="1" y="1" width="%d" height="%d" rx="16" fill="none" stroke="%s" stroke-opacity="0.45"/>
  %s
  <circle cx="%d" cy="%d" r="%d" fill="none" stroke="%s" stroke-opacity="0.5" stroke-width="1.5"/>
  <text x="%d" y="36" font-family="Segoe UI,Helvetica,Arial,sans-serif" font-size="15" font-weight="700" fill="%s">%s</text>
  <text x="%d" y="58" font-family="Segoe UI,Helvetica,Arial,sans-serif" font-size="12" fill="%s">%s</text>
  <text x="%d" y="88" font-family="Segoe UI,Helvetica,Arial,sans-serif" font-size="11" fill="%s">SKILL SCORE</text>
  <rect x="%d" y="96" width="%d" height="%d" rx="5" fill="#334155"/>
  <rect x="%d" y="96" width="%.1f" height="%d" rx="5" fill="url(#gg)"/>
  <text x="%d" y="106" font-family="Segoe UI,Helvetica,Arial,sans-serif" font-size="13" font-weight="700" fill="%s">%d/100</text>
  <text x="%d" y="132" font-family="Segoe UI,Helvetica,Arial,sans-serif" font-size="11" fill="%s">%s</text>
  <rect x="%d" y="14" width="%d" height="20" rx="10" fill="%s" stroke="%s" stroke-opacity="0.7"/>
  <text x="%d" y="28" text-anchor="end" font-family="Segoe UI,Helvetica,Arial,sans-serif" font-size="10" fill="%s">%s</text>
  <text x="%d" y="132" text-anchor="end" font-family="Segoe UI,Helvetica,Arial,sans-serif" font-size="10" fill="%s">BOT Chain</text>
  <a href="%s"><rect x="1" y="1" width="%d" height="%d" fill="transparent"/></a>
</svg>`,
		w, h, w, h, esc(b.GithubUsername),
		KawaiiTheme.Pink, KawaiiTheme.Lav, KawaiiTheme.Mint,
		w-2, h-2,
		w-2, h-2, KawaiiTheme.Card,
		h-2,
		w-2, h-2, KawaiiTheme.Pink,
		avatarMarkup,
		avatarCX, avatarCY, avatarSize/2+2, KawaiiTheme.Pink,
		textX, KawaiiTheme.Text, esc("@"+truncate(b.GithubUsername, 24)),
		textX, KawaiiTheme.Muted, esc(title),
		textX, KawaiiTheme.Muted,
		textX, barWidth, barHeight,
		textX, barW, barHeight,
		textX, KawaiiTheme.Mint, score,
		textX, KawaiiTheme.Muted, esc(skills),
		w-22-pillWidth, pillWidth, pillFill, pillStroke,
		w-30, pillStroke, esc(pillLabel),
		textX+barWidth, KawaiiTheme.Muted,
		esc(link), w-2, h-2,
	)
}

// Empty renders the "no badge yet" state.
func Empty() string {
	const w, h = 480, 110
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="No GGstar badge">
  <rect x="1" y="1" width="%d" height="%d" rx="16" fill="%s" stroke="%s" stroke-opacity="0.4"/>
  <text x="24" y="46" font-family="Segoe UI,Helvetica,Arial,sans-serif" font-size="15" font-weight="700" fill="%s">GGstar</text>
  <text x="24" y="72" font-family="Segoe UI,Helvetica,Arial,sans-serif" font-size="12" fill="%s">No soulbound badge yet - analyze a GitHub profile to mint one.</text>
  <text x="24" y="94" font-family="Segoe UI,Helvetica,Arial,sans-serif" font-size="10" fill="%s">powered by BOT Chain</text>
</svg>`, w, h, w, h, w-2, h-2, KawaiiTheme.Card, KawaiiTheme.Pink,
		KawaiiTheme.Text, KawaiiTheme.Muted, KawaiiTheme.Muted)
}

// Snippet returns copy-paste HTML/Markdown for GitHub READMEs.
func Snippet(b model.Badge, baseURL, explorerURL, contractAddress string) map[string]string {
	badgeURL := strings.TrimRight(baseURL, "/") + "/api/badge/" + b.Address + ".svg"
	verify := fmt.Sprintf("%s/token/%s?tokenId=%d", strings.TrimRight(explorerURL, "/"), contractAddress, b.TokenID)
	if contractAddress == "" {
		verify = explorerURL
	}

	htmlSnippet := fmt.Sprintf(`<a href="%s" target="_blank" rel="noopener">
  <img src="%s" alt="GGstar Skill Badge" width="480" />
</a>`, verify, badgeURL)

	mdSnippet := fmt.Sprintf(`[![GGstar Skill Badge](%s)](%s)`, badgeURL, verify)

	inline := fmt.Sprintf(`[![GGstar](%s)](%s)`, badgeURL, verify)

	return map[string]string{
		"badgeUrl": badgeURL,
		"verifyUrl": verify,
		"html":     htmlSnippet,
		"markdown": mdSnippet,
		"inline":   inline,
		"twitter":  buildTweet(b, verify),
	}
}

func buildTweet(b model.Badge, verifyURL string) string {
	title := b.CustomTitle
	if title == "" {
		title = "GGstar Badge Holder"
	}
	username := b.GithubUsername
	if username == "" {
		username = "a developer"
	}

	msg := fmt.Sprintf("I just minted my soulbound Skill Badge on BOT Chain as %q (skill score %d/100) for @%s.\n\nVerify on-chain: %s\n\n@BOTChain_ai #BOTChain #RWA #SBT",
		title, clampScore(b.SkillScore), username, verifyURL)

	return "https://twitter.com/intent/tweet?text=" + urlQueryEscape(msg)
}

// urlQueryEscape percent-encodes only the characters that would break a
// twitter.com/intent/tweet query string, leaving handles (@BOTChain_ai) and
// URLs readable.
func urlQueryEscape(s string) string {
	replacer := strings.NewReplacer(
		" ", "%20", "\n", "%0A", "\r", "%0D", "#", "%23", "&", "%26", "?", "%3F",
		"+", "%2B", "\"", "%22", "'", "%27", "<", "%3C", ">", "%3E", "`", "%60",
	)
	return replacer.Replace(s)
}
