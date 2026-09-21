package chain

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"ggstar/internal/model"

	"golang.org/x/crypto/sha3"
)

// Client performs read-only JSON-RPC calls against an EVM node (BOT Chain).
type Client struct {
	RPCURL          string
	ContractAddress string
	ExplorerURL     string
	HTTP            *http.Client
}

func New(rpcURL, contractAddress, explorerURL string) *Client {
	return &Client{
		RPCURL:          strings.TrimRight(rpcURL, "/"),
		ContractAddress: strings.TrimSpace(contractAddress),
		ExplorerURL:     strings.TrimRight(explorerURL, "/"),
		HTTP:            &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *Client) Configured() bool {
	return c.RPCURL != "" && isHexAddress(c.ContractAddress)
}

// Selector returns the 4-byte function selector for sig, e.g. "badgeStatus(address)".
// DecodeBadgeForTest exposes the ABI decoder to unit tests only.
func DecodeBadgeForTest(data []byte) (model.Badge, error) { return decodeBadge(data) }

func Selector(sig string) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(sig))
	return h.Sum(nil)[:4]
}

func isHexAddress(s string) bool {
	s = strings.TrimPrefix(s, "0x")
	if len(s) != 40 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func encodeAddress(addr string) []byte {
	raw, err := hex.DecodeString(strings.TrimPrefix(addr, "0x"))
	if err != nil || len(raw) != 20 {
		return make([]byte, 32)
	}
	out := make([]byte, 32)
	copy(out[12:], raw)
	return out
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	Result string `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) ethCall(ctx context.Context, callData []byte) ([]byte, error) {
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "eth_call",
		Params: []any{
			map[string]string{
				"to":   c.ContractAddress,
				"data": "0x" + hex.EncodeToString(callData),
			},
			"latest",
		},
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.RPCURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var parsed rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", parsed.Error.Code, parsed.Error.Message)
	}

	raw := strings.TrimPrefix(parsed.Result, "0x")
	if raw == "" {
		return nil, fmt.Errorf("empty rpc result")
	}
	out, err := hex.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func word(data []byte, index int) ([]byte, bool) {
	start := index * 32
	if start+32 > len(data) {
		return nil, false
	}
	return data[start : start+32], true
}

func wordUint(data []byte, index int) (*big.Int, bool) {
	w, ok := word(data, index)
	if !ok {
		return nil, false
	}
	return new(big.Int).SetBytes(w), true
}

func decodeString(data []byte, offset int) (string, error) {
	if offset+32 > len(data) {
		return "", fmt.Errorf("string offset out of range")
	}
	length := new(big.Int).SetBytes(data[offset : offset+32]).Int64()
	if length < 0 {
		return "", fmt.Errorf("negative string length")
	}
	start := offset + 32
	end := start + int(length)
	if end > len(data) || end < start {
		return "", fmt.Errorf("string length out of range")
	}
	return string(data[start:end]), nil
}

func decodeStringArray(data []byte, offset int) ([]string, error) {
	if offset+32 > len(data) {
		return nil, fmt.Errorf("array offset out of range")
	}
	count := new(big.Int).SetBytes(data[offset : offset+32]).Int64()
	if count < 0 || count > 4096 {
		return nil, fmt.Errorf("invalid array length %d", count)
	}

	head := offset + 32
	out := make([]string, 0, count)
	for i := 0; i < int(count); i++ {
		rel, ok := wordUint(data, (head/32)+i)
		if !ok {
			return nil, fmt.Errorf("array element offset out of range")
		}
		s, err := decodeString(data, head+int(rel.Int64()))
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// decodeBadge decodes the BadgeData tuple returned by getBadgeByAddress.
//
// Solidity may encode a single dynamic struct either wrapped in an outer offset
// word (0x20) or directly; both layouts are handled here.
func decodeBadge(data []byte) (model.Badge, error) {
	var badge model.Badge

	if len(data) < 32 {
		return badge, fmt.Errorf("return data too short")
	}

	base := 0
	if first, _ := wordUint(data, 0); first != nil && first.Int64() == 32 {
		base = 32
	}

	idx := base / 32

	userOff, ok := wordUint(data, idx)
	if !ok {
		return badge, fmt.Errorf("missing githubUsername offset")
	}
	avatarID, ok := wordUint(data, idx+1)
	if !ok {
		return badge, fmt.Errorf("missing avatarId")
	}
	titleOff, ok := wordUint(data, idx+2)
	if !ok {
		return badge, fmt.Errorf("missing customTitle offset")
	}
	score, ok := wordUint(data, idx+3)
	if !ok {
		return badge, fmt.Errorf("missing skillScore")
	}
	skillsOff, ok := wordUint(data, idx+4)
	if !ok {
		return badge, fmt.Errorf("missing skills offset")
	}
	mintedAt, ok := wordUint(data, idx+5)
	if !ok {
		return badge, fmt.Errorf("missing mintedAt")
	}

	username, err := decodeString(data, base+int(userOff.Int64()))
	if err != nil {
		return badge, err
	}
	title, err := decodeString(data, base+int(titleOff.Int64()))
	if err != nil {
		return badge, err
	}
	skills, err := decodeStringArray(data, base+int(skillsOff.Int64()))
	if err != nil {
		return badge, err
	}

	badge.Found = true
	badge.GithubUsername = username
	badge.AvatarID = avatarID.Uint64()
	badge.CustomTitle = title
	badge.SkillScore = int(score.Int64())
	badge.Skills = skills
	badge.MintedAt = mintedAt.Uint64()
	return badge, nil
}

// GetBadge reads a wallet's soulbound badge. Returns Found=false when the wallet
// has never minted (checked via the static badgeStatus view first).
func (c *Client) GetBadge(ctx context.Context, address string) (model.Badge, error) {
	if !c.Configured() {
		return model.Badge{}, fmt.Errorf("contract address is not configured")
	}
	if !isHexAddress(address) {
		return model.Badge{}, fmt.Errorf("invalid address %q", address)
	}

	statusData := append(Selector("badgeStatus(address)"), encodeAddress(address)...)
	statusRaw, err := c.ethCall(ctx, statusData)
	if err != nil {
		return model.Badge{}, err
	}

	hasBadgeWord, ok := wordUint(statusRaw, 0)
	if !ok {
		return model.Badge{}, fmt.Errorf("unexpected badgeStatus response")
	}
	if hasBadgeWord.Sign() == 0 {
		return model.Badge{Found: false}, nil
	}

	tokenID, _ := wordUint(statusRaw, 1)

	badgeData := append(Selector("getBadgeByAddress(address)"), encodeAddress(address)...)
	raw, err := c.ethCall(ctx, badgeData)
	if err != nil {
		return model.Badge{}, err
	}

	badge, err := decodeBadge(raw)
	if err != nil {
		return model.Badge{}, err
	}

	badge.Address = address
	if tokenID != nil {
		badge.TokenID = tokenID.Uint64()
		badge.ExplorerURL = fmt.Sprintf("%s/token/%s?tokenId=%d", c.ExplorerURL, c.ContractAddress, badge.TokenID)
	}
	return badge, nil
}

// HexChainID queries eth_chainId and returns the decimal value.
func (c *Client) HexChainID(ctx context.Context) (int64, error) {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: "eth_chainId", Params: []any{}})
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.RPCURL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var parsed rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return 0, err
	}
	if parsed.Error != nil {
		return 0, fmt.Errorf("rpc error: %s", parsed.Error.Message)
	}

	raw := strings.TrimPrefix(parsed.Result, "0x")
	if raw == "" {
		return 0, fmt.Errorf("empty chain id")
	}
	value, ok := new(big.Int).SetString(strings.TrimLeft(raw, "0"), 16)
	if !ok {
		if strings.Trim(raw, "0") == "" {
			return 0, nil
		}
		return 0, fmt.Errorf("invalid chain id %q", parsed.Result)
	}
	return value.Int64(), nil
}
