package chain

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// abiWord encodes a uint256.
func abiWord(n int) string {
	return fmt.Sprintf("%064x", n)
}

func abiPadTo32(b []byte) string {
	pad := 32 - (len(b) % 32)
	if pad == 32 {
		pad = 0
	}
	return hex.EncodeToString(b) + strings.Repeat("00", pad)
}

func abiString(s string) string {
	raw := []byte(s)
	return abiWord(len(raw)) + abiPadTo32(raw)
}

// buildBadgeReturn mirrors Solidity ABI encoding of
// getBadgeByAddress(address) returns (BadgeData memory).
func buildBadgeReturn(username, title string, avatarID, score, mintedAt int, skills []string) string {
	uTail := abiString(username)
	tTail := abiString(title)

	// skills array tail: length + offsets + elements
	skillsTail := abiWord(len(skills))
	elemTails := make([]string, len(skills))
	elemsOffset := 32 * len(skills)
	for i, s := range skills {
		e := abiString(s)
		skillsTail += abiWord(elemsOffset)
		elemTails[i] = e
		elemsOffset += len(e) / 2
	}
	for _, e := range elemTails {
		skillsTail += e
	}

	// struct head: usernameOff, avatarId, titleOff, skillScore, skillsOff, mintedAt
	headLen := 6 * 32
	uOff := headLen
	tOff := uOff + len(uTail)/2
	sOff := tOff + len(tTail)/2

	head := abiWord(uOff) + abiWord(avatarID) + abiWord(tOff) + abiWord(score) + abiWord(sOff) + abiWord(mintedAt)

	// Outer offset word (0x20) because Solidity wraps a single dynamic struct.
	return "0x" + abiWord(32) + head + uTail + tTail + skillsTail
}

func TestGetBadgeDecodesProjectStruct(t *testing.T) {
	want := struct {
		user    string
		title   string
		avatar  uint64
		score   int
		skills  []string
		minted  uint64
		tokenID uint64
	}{
		user: "torvalds", title: "Solidity Princess \U0001F451",
		avatar: 42, score: 88,
		skills: []string{"Solidity", "Go", "Rust"}, minted: 1735689600, tokenID: 7,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string   `json:"method"`
			Params []any    `json:"params"`
			ID     int      `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		call, _ := req.Params[0].(map[string]any)
		data, _ := call["data"].(string)

		var result string
		switch {
		case strings.HasPrefix(data, "0x"+hex.EncodeToString(Selector("badgeStatus(address)"))):
			// (true, tokenId)
			result = "0x" + abiWord(1) + abiWord(int(want.tokenID))
		case strings.HasPrefix(data, "0x"+hex.EncodeToString(Selector("getBadgeByAddress(address)"))):
			result = buildBadgeReturn(want.user, want.title, int(want.avatar), want.score, int(want.minted), want.skills)
		default:
			t.Errorf("unexpected call data %q", data)
			result = "0x"
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"result":%q}`, result)))
	}))
	defer srv.Close()

	const contract = "0x1234567890abcdef1234567890abcdef12345678"
	const wallet = "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"

	client := New(srv.URL, contract, "https://scan.bohr.life")

	badge, err := client.GetBadge(context.Background(), wallet)
	if err != nil {
		t.Fatalf("GetBadge error: %v", err)
	}

	if !badge.Found {
		t.Fatal("expected Found=true")
	}
	if badge.GithubUsername != want.user {
		t.Errorf("githubUsername = %q, want %q", badge.GithubUsername, want.user)
	}
	if badge.CustomTitle != want.title {
		t.Errorf("customTitle = %q, want %q", badge.CustomTitle, want.title)
	}
	if badge.AvatarID != want.avatar {
		t.Errorf("avatarId = %d, want %d", badge.AvatarID, want.avatar)
	}
	if badge.SkillScore != want.score {
		t.Errorf("skillScore = %d, want %d", badge.SkillScore, want.score)
	}
	if badge.MintedAt != want.minted {
		t.Errorf("mintedAt = %d, want %d", badge.MintedAt, want.minted)
	}
	if badge.TokenID != want.tokenID {
		t.Errorf("tokenId = %d, want %d", badge.TokenID, want.tokenID)
	}
	if len(badge.Skills) != len(want.skills) {
		t.Fatalf("skills len = %d (%v), want %d (%v)", len(badge.Skills), badge.Skills, len(want.skills), want.skills)
	}
	for i := range want.skills {
		if badge.Skills[i] != want.skills[i] {
			t.Errorf("skills[%d] = %q, want %q", i, badge.Skills[i], want.skills[i])
		}
	}
	if badge.ExplorerURL == "" {
		t.Error("expected explorer URL to be built")
	}
}

func TestGetBadgeNoBadge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// badgeStatus returns (false, 0)
		_, _ = w.Write([]byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":\"0x" + abiWord(0) + abiWord(0) + "\"}"))
	}))
	defer srv.Close()

	client := New(srv.URL, "0x1234567890abcdef1234567890abcdef12345678", "https://scan.bohr.life")

	badge, err := client.GetBadge(context.Background(), "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if badge.Found {
		t.Error("expected Found=false for a wallet without a badge")
	}
}

func TestHexChainIDShortValue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x3c8"}`))
	}))
	defer srv.Close()

	client := New(srv.URL, "", "")
	id, err := client.HexChainID(context.Background())
	if err != nil {
		t.Fatalf("HexChainID error: %v", err)
	}
	if id != 968 {
		t.Errorf("chain id = %d, want 968", id)
	}
}

func TestSelectorMatchesKeccak(t *testing.T) {
	// Known selector: transfer(address,uint256) = 0xa9059cbb
	got := hex.EncodeToString(Selector("transfer(address,uint256)"))
	if got != "a9059cbb" {
		t.Errorf("selector = %s, want a9059cbb", got)
	}
}

func TestDecodeBadgeWithoutOuterOffset(t *testing.T) {
	// Some nodes return the struct head directly (no wrapping 0x20 word).
	raw := buildBadgeReturn("octocat", "Mint Wizard", 3, 71, 1700000000, []string{"Go"})
	raw = raw[2:]
	// strip leading offset word
	raw = raw[64:]
	decoded, err := hex.DecodeString(raw)
	if err != nil {
		t.Fatal(err)
	}

	client := New("http://unused", "0x1234567890abcdef1234567890abcdef12345678", "https://scan.bohr.life")
	_ = client

	badge, err := decodeBadge(decoded)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if badge.GithubUsername != "octocat" || badge.SkillScore != 71 || badge.AvatarID != 3 {
		t.Errorf("unexpected decode: %+v", badge)
	}
}

func TestBigIntChainIDParsing(t *testing.T) {
	if v, ok := new(big.Int).SetString("3c8", 16); !ok || v.Int64() != 968 {
		t.Fatal("sanity check for chain id parsing failed")
	}
}
