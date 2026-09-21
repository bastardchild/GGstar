package chain

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testContract = "0x1234567890abcdef1234567890abcdef12345678"

// buildBadgeMintedLog mirrors Solidity's log layout for
// BadgeMinted(address indexed owner, uint256 indexed tokenId, string githubUsername, uint256 avatarId, uint8 skillScore)
func buildBadgeMintedLog(owner string, tokenID uint64, username string, avatarID uint64, score int) Log {
	topic0 := "0x" + hex.EncodeToString(BadgeMintedTopic())

	ownerHex := strings.TrimPrefix(strings.ToLower(owner), "0x")
	ownerWord := strings.Repeat("0", 24) + ownerHex

	// data = abi.encode(username, avatarID, skillScore)
	usernameTail := abiString(username)
	data := abiWord(3*32) + abiWord(int(avatarID)) + abiWord(score) + usernameTail

	return Log{
		Address: testContract,
		Topics:  []string{topic0, "0x" + ownerWord, "0x" + abiWord(int(tokenID))},
		Data:    "0x" + data,
	}
}

func TestBadgeMintedTopicIsStable(t *testing.T) {
	// keccak256("BadgeMinted(address,uint256,string,uint256,uint8)"). Pinning the
	// exact value catches accidental signature drift between the contract and the
	// decoder. The same keccak implementation is proven against the well-known
	// transfer(address,uint256) selector in TestSelectorMatchesKeccak.
	const want = "1ffb3a9c"

	if got := hex.EncodeToString(BadgeMintedTopic()); got != want {
		t.Errorf("BadgeMinted topic = %s, want %s", got, want)
	}
}

func TestFindBadgeMintedDecodesEvent(t *testing.T) {
	receipt := Receipt{
		Status: "0x1",
		Logs: []Log{
			// An unrelated log from another contract must be skipped.
			{Address: "0x9999999999999999999999999999999999999999", Topics: []string{"0x" + strings.Repeat("11", 32)}},
			buildBadgeMintedLog("0xAbC1230000000000000000000000000000004567", 9, "torvalds", 42, 88),
		},
	}

	got, found := receipt.FindBadgeMinted(testContract)
	if !found {
		t.Fatal("expected to find the BadgeMinted event")
	}
	if got.TokenID != 9 {
		t.Errorf("tokenId = %d, want 9", got.TokenID)
	}
	if got.GithubUsername != "torvalds" {
		t.Errorf("githubUsername = %q, want torvalds", got.GithubUsername)
	}
	if got.AvatarID != 42 {
		t.Errorf("avatarId = %d, want 42", got.AvatarID)
	}
	if got.SkillScore != 88 {
		t.Errorf("skillScore = %d, want 88", got.SkillScore)
	}
	if !strings.EqualFold(got.Owner, "0xAbC1230000000000000000000000000000004567") {
		t.Errorf("owner = %q", got.Owner)
	}
}

func TestFindBadgeMintedIgnoresForeignContract(t *testing.T) {
	receipt := Receipt{
		Status: "0x1",
		Logs:   []Log{buildBadgeMintedLog("0xabc", 1, "someone", 1, 50)},
	}
	receipt.Logs[0].Address = "0x9999999999999999999999999999999999999999"

	if _, found := receipt.FindBadgeMinted(testContract); found {
		t.Error("events from another contract must not be accepted")
	}
}

func TestFindBadgeMintedIgnoresWrongTopic(t *testing.T) {
	log := buildBadgeMintedLog("0xabc", 1, "someone", 1, 50)
	log.Topics[0] = "0x" + strings.Repeat("22", 32)

	receipt := Receipt{Status: "0x1", Logs: []Log{log}}
	if _, found := receipt.FindBadgeMinted(testContract); found {
		t.Error("logs with a different signature must be skipped")
	}
}

func TestFindBadgeMintedHandlesMalformedLog(t *testing.T) {
	malformed := buildBadgeMintedLog("0xabc", 1, "x", 1, 50)
	malformed.Data = "0xdeadbeef" // too short

	receipt := Receipt{Status: "0x1", Logs: []Log{malformed}}
	if _, found := receipt.FindBadgeMinted(testContract); found {
		t.Error("malformed log data must not be decoded")
	}
}

func TestGetTransactionReceiptPending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
	}))
	defer srv.Close()

	client := New(srv.URL, testContract, "https://scan.bohr.life")

	receipt, err := client.GetTransactionReceipt(context.Background(), "0x"+strings.Repeat("ab", 32))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receipt != nil {
		t.Error("a pending transaction should return a nil receipt, not an error")
	}
}

func TestGetTransactionReceiptDecodes(t *testing.T) {
	log := buildBadgeMintedLog("0xAbC1230000000000000000000000000000004567", 3, "octocat", 7, 70)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{
				"status": "0x1",
				"from":   "0xAbC1230000000000000000000000000000004567",
				"to":     testContract,
				"logs":   []Log{log},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	client := New(srv.URL, testContract, "https://scan.bohr.life")

	receipt, err := client.GetTransactionReceipt(context.Background(), "0x"+strings.Repeat("cd", 32))
	if err != nil {
		t.Fatalf("GetTransactionReceipt: %v", err)
	}
	if receipt == nil {
		t.Fatal("expected a receipt")
	}
	if receipt.Failed() {
		t.Error("status 0x1 should not be reported as failed")
	}

	minted, found := receipt.FindBadgeMinted(testContract)
	if !found {
		t.Fatal("expected the event to be found")
	}
	if minted.GithubUsername != "octocat" || minted.TokenID != 3 || minted.SkillScore != 70 {
		t.Errorf("unexpected decode: %+v", minted)
	}
}

func TestGetTransactionReceiptRejectsBadHash(t *testing.T) {
	client := New("http://unused", testContract, "")

	for _, bad := range []string{"", "0x123", "nothex", "0x" + strings.Repeat("zz", 32)} {
		if _, err := client.GetTransactionReceipt(context.Background(), bad); err == nil {
			t.Errorf("hash %q should be rejected", bad)
		}
	}
}

func TestReceiptFailed(t *testing.T) {
	if (&Receipt{Status: "0x1"}).Failed() {
		t.Error("0x1 means success")
	}
	if !(&Receipt{Status: "0x0"}).Failed() {
		t.Error("0x0 means reverted")
	}
	// An empty status (some nodes omit it) must not be treated as failure.
	if (&Receipt{}).Failed() {
		t.Error("missing status should not be treated as a revert")
	}
}
