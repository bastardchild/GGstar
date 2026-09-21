package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"ggstar/internal/cache"
	"ggstar/internal/config"
	"ggstar/internal/db"

	"ggstar/internal/chain"
)

const (
	testnetContract = "0x1234567890abcdef1234567890abcdef12345678"
	mainnetContract = "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"
	wallet          = "0x1111111111111111111111111111111111111111"
)

// abiWord/like helpers are redefined here because the chain package keeps its
// own copies internal to its tests.
func word(n int) string { return fmt.Sprintf("%064x", n) }

func strTail(s string) string {
	raw := []byte(s)
	out := hex.EncodeToString(raw)
	if pad := len(out) % 64; pad != 0 {
		out += strings.Repeat("0", 64-pad)
	}
	return word(len(raw)) + out
}

// stubRPC returns a JSON-RPC server that answers badgeStatus/getBadgeByAddress.
// When hasBadge is false it reports "no badge" for that network.
func stubRPC(t *testing.T, hasBadge bool) (*httptest.Server, *int32) {
	t.Helper()

	var calls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)

		var req struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		if req.Method != "eth_call" {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x"}`))
			return
		}

		call, _ := req.Params[0].(map[string]any)
		data, _ := call["data"].(string)

		var result string
		switch {
		case strings.HasPrefix(data, "0x"+hex.EncodeToString(chain.Selector("badgeStatus(address)"))):
			if hasBadge {
				// (true, tokenId=5)
				result = "0x" + word(1) + word(5)
			} else {
				result = "0x" + word(0) + word(0)
			}
		case strings.HasPrefix(data, "0x"+hex.EncodeToString(chain.Selector("getBadgeByAddress(address)"))):
			username := strTail("alice")
			title := strTail("Badge Holder")
			// head: usernameOff, avatarId, titleOff, score, skillsOff, mintedAt
			userOff := word(6 * 32)
			titleOff := word(6*32 + len(username)/2)
			skillsOff := word(6*32 + len(username)/2 + len(title)/2)
			skills := word(1) + word(32) + strTail("Go")
			result = "0x" + word(32) +
				userOff + word(3) + titleOff + word(77) + skillsOff + word(1700000000) +
				username + title + skills
		default:
			result = "0x"
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":%q}`, result)
	}))

	t.Cleanup(srv.Close)
	return srv, &calls
}

func newServiceForChain(t *testing.T, testnetRPC, mainnetRPC, testnetAddr, mainnetAddr string) *Service {
	t.Helper()

	store, err := db.Open(filepath.Join(t.TempDir(), "svc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cacheStore, err := cache.New(context.Background(), cache.Config{Driver: cache.DriverMemory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cacheStore.Close() })

	cfg := config.Config{
		RPCURL:              testnetRPC,
		ContractAddr:        testnetAddr,
		ExplorerURL:         "https://scan.bohr.life",
		MainnetRPCURL:       mainnetRPC,
		MainnetContractAddr: mainnetAddr,
		MainnetExplorerURL:  "https://scan.botchain.ai",
	}

	return New(cfg, store, cacheStore)
}

// TestGetBadgePrefersMainnet proves the launched contract is what visitors see.
func TestGetBadgePrefersMainnet(t *testing.T) {
	testnetSrv, testnetCalls := stubRPC(t, true)
	mainnetSrv, _ := stubRPC(t, true)

	svc := newServiceForChain(t, testnetSrv.URL, mainnetSrv.URL, testnetContract, mainnetContract)

	badge, err := svc.GetBadge(context.Background(), wallet)
	if err != nil {
		t.Fatalf("GetBadge: %v", err)
	}
	if !badge.Found {
		t.Fatal("expected a badge")
	}
	if badge.ExplorerURL == "" || !strings.Contains(badge.ExplorerURL, "scan.botchain.ai") {
		t.Errorf("badge should come from mainnet, got explorer %q", badge.ExplorerURL)
	}
	if n := atomic.LoadInt32(testnetCalls); n != 0 {
		t.Errorf("testnet must not be queried when mainnet answers, got %d calls", n)
	}
}

// TestGetBadgeFallsBackToTestnet keeps pre-launch badges visible.
func TestGetBadgeFallsBackToTestnet(t *testing.T) {
	testnetSrv, _ := stubRPC(t, true)
	mainnetSrv, _ := stubRPC(t, false) // no badge on mainnet

	svc := newServiceForChain(t, testnetSrv.URL, mainnetSrv.URL, testnetContract, mainnetContract)

	badge, err := svc.GetBadge(context.Background(), wallet)
	if err != nil {
		t.Fatalf("GetBadge: %v", err)
	}
	if !badge.Found {
		t.Fatal("expected the testnet badge to be found via fallback")
	}
	if !strings.Contains(badge.ExplorerURL, "scan.bohr.life") {
		t.Errorf("badge should be reported on testnet, got %q", badge.ExplorerURL)
	}
}

// TestGetBadgeWithoutMainnetIgnoresIt covers a pre-launch deployment.
func TestGetBadgeWithoutMainnetIgnoresIt(t *testing.T) {
	testnetSrv, testnetCalls := stubRPC(t, true)

	svc := newServiceForChain(t, testnetSrv.URL, "http://127.0.0.1:1", testnetContract, "")

	badge, err := svc.GetBadge(context.Background(), wallet)
	if err != nil {
		t.Fatalf("GetBadge: %v", err)
	}
	if !badge.Found {
		t.Fatal("expected the testnet badge")
	}
	if atomic.LoadInt32(testnetCalls) == 0 {
		t.Error("testnet should have been queried")
	}
}

// TestGetBadgeConfiguredOnNeitherNetwork is the honest failure mode.
func TestGetBadgeConfiguredOnNeitherNetwork(t *testing.T) {
	svc := newServiceForChain(t, "http://127.0.0.1:1", "http://127.0.0.1:1", "", "")

	_, err := svc.GetBadge(context.Background(), wallet)
	if err == nil {
		t.Fatal("expected an error when no contract is configured")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("error should say the contract is not configured, got %v", err)
	}
}
