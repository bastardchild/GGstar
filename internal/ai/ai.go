package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"ggstar/internal/model"
)

type Result struct {
	SkillScore      int      `json:"skillScore"`
	TopSkills       []string `json:"topSkills"`
	SuggestedTitles []string `json:"suggestedTitles"`
	Summary         string   `json:"summary"`
}

type Input struct {
	Username      string
	Name          string
	Bio           string
	Company       string
	Location      string
	PublicRepos   int
	Followers     int
	AccountYears  float64
	TotalStars    int
	MaxRepoStars  int
	DominantLang  string
	Languages     []string
	TopRepos      []RepoBrief
	TopTopics     []string
}

type RepoBrief struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Language    string   `json:"language"`
	Stars       int      `json:"stars"`
	Topics      []string `json:"topics"`
}

type Client struct {
	BaseURL   string
	APIKey    string
	Model     string
	Timeout   time.Duration
	MaxTokens int

	HTTP *http.Client
}

func New(baseURL, apiKey, model string, timeout time.Duration, maxTokens int) *Client {
	if maxTokens <= 0 {
		maxTokens = 900
	}
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		APIKey:    apiKey,
		Model:     model,
		Timeout:   timeout,
		MaxTokens: maxTokens,
		HTTP:      &http.Client{Timeout: timeout},
	}
}

func (c *Client) Enabled() bool { return c.APIKey != "" }

const systemPrompt = `You are a senior technical recruiter who evaluates GitHub profiles.
Given structured GitHub data, respond with ONLY a compact JSON object using this exact shape:
{"skillScore": <integer 1-100>, "topSkills": ["...", "..."], "suggestedTitles": ["...","...","...","..."], "summary": "one or two sentences"}
Rules:
- skillScore must be an integer between 1 and 100 (never 0, never >100).
- topSkills: 3 to 6 concrete technologies, ordered by strength, no duplicates.
- suggestedTitles: exactly 4 playful titles (emoji allowed) that fit the profile.
- summary: max 220 characters, mention the dominant stack and impact.
- Output raw JSON only. No markdown fences, no commentary.`

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	Temperature    float64         `json:"temperature"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Analyze calls an OpenAI-compatible /chat/completions endpoint. On any failure
// it returns a deterministic mock result so the demo never breaks.
func (c *Client) Analyze(ctx context.Context, in Input) (Result, string) {
	if !c.Enabled() {
		return Mock(in), "mock"
	}

	payload, err := json.Marshal(in)
	if err != nil {
		return Mock(in), "mock"
	}

	body, err := json.Marshal(chatRequest{
		Model:       c.Model,
		Temperature: 0.4,
		MaxTokens:   c.MaxTokens,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: "GitHub profile data:\n" + string(payload)},
		},
		ResponseFormat: &responseFormat{Type: "json_object"},
	})
	if err != nil {
		return Mock(in), "mock"
	}

	reqCtx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Mock(in), "mock"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Mock(in), "mock"
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Mock(in), "mock"
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Mock(in), "mock"
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.Choices) == 0 {
		return Mock(in), "mock"
	}

	content := StripFences(parsed.Choices[0].Message.Content)
	var out Result
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return Mock(in), "mock"
	}

	out = Normalize(out, in)
	return out, "ai"
}

var fenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)\\s*```")

// StripFences removes markdown code fences and leading prose that models often
// wrap JSON in.
func StripFences(s string) string {
	s = strings.TrimSpace(s)
	if m := fenceRe.FindStringSubmatch(s); len(m) == 2 {
		s = strings.TrimSpace(m[1])
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		s = s[start : end+1]
	}
	return strings.TrimSpace(s)
}

// Normalize clamps the score into 1..100, dedupes skills and guarantees 4 titles.
func Normalize(r Result, in Input) Result {
	if r.SkillScore < 1 {
		r.SkillScore = 1
	}
	if r.SkillScore > 100 {
		r.SkillScore = 100
	}

	seen := map[string]bool{}
	skills := make([]string, 0, len(r.TopSkills))
	for _, s := range r.TopSkills {
		s = strings.TrimSpace(s)
		if s == "" || seen[strings.ToLower(s)] {
			continue
		}
		seen[strings.ToLower(s)] = true
		skills = append(skills, s)
		if len(skills) == 6 {
			break
		}
	}
	if len(skills) == 0 {
		skills = fallbackSkills(in)
	}
	r.TopSkills = skills

	titles := make([]string, 0, 4)
	for _, t := range r.SuggestedTitles {
		t = strings.TrimSpace(t)
		if t != "" {
			titles = append(titles, t)
		}
		if len(titles) == 4 {
			break
		}
	}
	for _, t := range fallbackTitles(in) {
		if len(titles) == 4 {
			break
		}
		titles = append(titles, t)
	}
	r.SuggestedTitles = titles

	r.Summary = strings.TrimSpace(r.Summary)
	if r.Summary == "" {
		r.Summary = mockSummary(in)
	}
	if len(r.Summary) > 400 {
		r.Summary = r.Summary[:400]
	}
	return r
}

func fallbackSkills(in Input) []string {
	out := []string{}
	for _, l := range in.Languages {
		if l != "" {
			out = append(out, l)
		}
		if len(out) == 5 {
			break
		}
	}
	if len(out) == 0 {
		out = []string{"Go", "JavaScript", "Git"}
	}
	return out
}

func fallbackTitles(in Input) []string {
	lead := in.DominantLang
	if lead == "" {
		lead = "Code"
	}
	return []string{
		lead + " Adventurer",
		"Vibe Coding Queen",
		"Open Source Sprout",
		"BOT Chain Builder",
	}
}

func mockSummary(in Input) string {
	lang := in.DominantLang
	if lang == "" {
		lang = "polyglot"
	}
	return fmt.Sprintf("%s ships mostly %s across %d public repos with %d stars, and is ready to mint a reputation badge on BOT Chain.",
		displayName(in), lang, in.PublicRepos, in.TotalStars)
}

func displayName(in Input) string {
	if strings.TrimSpace(in.Name) != "" {
		return in.Name
	}
	return in.Username
}

// Mock produces a deterministic, data-driven fallback result.
func Mock(in Input) Result {
	score := int(math.Round(
		18 +
			math.Min(float64(in.TotalStars), 3000)/3000*34 +
			math.Min(float64(in.PublicRepos), 80)/80*18 +
			math.Min(float64(in.Followers), 800)/800*14 +
			math.Min(float64(len(in.Languages)), 8)/8*10 +
			math.Min(in.AccountYears, 12)/12*6,
	))
	if score > 100 {
		score = 100
	}
	if score < 1 {
		score = 1
	}

	return Normalize(Result{
		SkillScore:      score,
		TopSkills:       fallbackSkills(in),
		SuggestedTitles: fallbackTitles(in),
		Summary:         mockSummary(in),
	}, in)
}

// ToAnalysis merges an AI result into the profile model.
func ToAnalysis(in Input, r Result, source string) model.Analysis {
	return model.Analysis{
		Username:        in.Username,
		AvatarURL:       "",
		Name:            in.Name,
		Bio:             in.Bio,
		SkillScore:      r.SkillScore,
		TopSkills:       r.TopSkills,
		SuggestedTitles: r.SuggestedTitles,
		Summary:         r.Summary,
		DominantLang:    in.DominantLang,
		PublicRepos:     in.PublicRepos,
		Followers:       in.Followers,
		TotalStars:      in.TotalStars,
		Source:          source,
		UpdatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
}
