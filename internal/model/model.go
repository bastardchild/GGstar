package model

// Analysis is the AI-generated reputation result for a GitHub profile.
type Analysis struct {
	Username        string   `json:"username"`
	AvatarURL       string   `json:"avatarUrl"`
	Name            string   `json:"name"`
	Bio             string   `json:"bio"`
	SkillScore      int      `json:"skillScore"`
	TopSkills       []string `json:"topSkills"`
	SuggestedTitles []string `json:"suggestedTitles"`
	Summary         string   `json:"summary"`
	DominantLang    string   `json:"dominantLanguage"`

	PublicRepos int `json:"publicRepos"`
	Followers   int `json:"followers"`
	TotalStars  int `json:"totalStars"`

	Source    string `json:"source"` // "ai" | "mock" | "cache" | "db"
	UpdatedAt string `json:"updatedAt"`
}

// ProfileRow mirrors a row of the profiles table.
type ProfileRow struct {
	Username        string
	SkillScore      int
	TopSkills       []string
	SuggestedTitles []string
	Summary         string
	DominantLang    string
	TotalStars      int
	StarsUpdatedAt  string
	UpdatedAt       string
}

// Badge is the on-chain payload returned by getBadgeByAddress.
type Badge struct {
	Found          bool     `json:"found"`
	Address        string   `json:"address"`
	TokenID        uint64   `json:"tokenId"`
	GithubUsername string   `json:"githubUsername"`
	AvatarID       uint64   `json:"avatarId"`
	CustomTitle    string   `json:"customTitle"`
	SkillScore     int      `json:"skillScore"`
	Skills         []string `json:"skills"`
	MintedAt       uint64   `json:"mintedAt"`
	ExplorerURL    string   `json:"explorerUrl"`

	// Verified is not part of the on-chain struct. It is filled in by the
	// application after matching the badge against a signed-in GitHub identity
	// (see service.VerifyClaim).
	Verified bool `json:"verified"`
	// VerifiedLogin is the OAuth login that proved ownership, when Verified.
	VerifiedLogin string `json:"verifiedLogin,omitempty"`
	// VerifyReason explains why a claim is unverified.
	VerifyReason string `json:"verifyReason,omitempty"`
}
