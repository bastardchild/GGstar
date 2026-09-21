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
)

// Receipt is the subset of eth_getTransactionReceipt ggstar needs.
type Receipt struct {
	Status string `json:"status"`
	From   string `json:"from"`
	To     string `json:"to"`
	Logs   []Log  `json:"logs"`
}

type Log struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    string   `json:"data"`
}

// BadgeMinted is the decoded payload of the contract's BadgeMinted event.
type BadgeMinted struct {
	Owner          string
	TokenID        uint64
	GithubUsername string
	AvatarID       uint64
	SkillScore     int
}

// BadgeMintedTopic is keccak256("BadgeMinted(address,uint256,string,uint256,uint8)").
// It is derived at call time so it always matches the compiled contract.
func BadgeMintedTopic() []byte {
	return Selector("BadgeMinted(address,uint256,string,uint256,uint8)")
}

// rpcRaw performs a JSON-RPC call and returns the raw result payload, which may
// be null for not-yet-mined transactions.
func (c *Client) rpcRaw(ctx context.Context, method string, params []any) (json.RawMessage, error) {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
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

	var parsed struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", parsed.Error.Code, parsed.Error.Message)
	}
	return parsed.Result, nil
}

// GetTransactionReceipt fetches a receipt. A nil receipt with nil error means the
// transaction is still pending.
func (c *Client) GetTransactionReceipt(ctx context.Context, txHash string) (*Receipt, error) {
	if !isTxHash(txHash) {
		return nil, fmt.Errorf("invalid transaction hash %q", txHash)
	}

	raw, err := c.rpcRaw(ctx, "eth_getTransactionReceipt", []any{txHash})
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var receipt Receipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}

func isTxHash(s string) bool {
	s = strings.TrimPrefix(s, "0x")
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// Failed reports whether the transaction reverted.
func (r *Receipt) Failed() bool {
	return r.Status != "" && r.Status != "0x1"
}

// FindBadgeMinted extracts the first BadgeMinted event emitted by the configured
// contract. Non-matching logs are skipped, so unrelated events are harmless.
func (r *Receipt) FindBadgeMinted(contractAddress string) (BadgeMinted, bool) {
	want := hex.EncodeToString(BadgeMintedTopic())

	for _, log := range r.Logs {
		if !strings.EqualFold(log.Address, contractAddress) {
			continue
		}
		if len(log.Topics) < 3 {
			continue
		}
		if strings.ToLower(strings.TrimPrefix(log.Topics[0], "0x")) != want {
			continue
		}

		decoded, err := decodeBadgeMintedLog(log)
		if err != nil {
			continue
		}
		return decoded, true
	}
	return BadgeMinted{}, false
}

// decodeBadgeMintedLog decodes the non-indexed fields of BadgeMinted.
//
// Layout:
//
//	topic0 = event signature
//	topic1 = owner  (indexed, 32-byte ABI word holding a left-padded address)
//	topic2 = tokenId (indexed)
//	data   = abi.encode(githubUsername, avatarId, skillScore)
//
// githubUsername is dynamic, so the data section starts with a tuple head whose
// first word is an offset to the string, followed by avatarId and skillScore.
func decodeBadgeMintedLog(log Log) (BadgeMinted, error) {
	var out BadgeMinted

	data, err := hex.DecodeString(strings.TrimPrefix(log.Data, "0x"))
	if err != nil {
		return out, fmt.Errorf("decode log data: %w", err)
	}

	ownerTopic, err := hex.DecodeString(strings.TrimPrefix(log.Topics[1], "0x"))
	if err != nil {
		return out, fmt.Errorf("decode owner topic: %w", err)
	}
	ownerWord, ok := word(ownerTopic, 0)
	if !ok {
		return out, fmt.Errorf("missing owner topic")
	}
	out.Owner = "0x" + hex.EncodeToString(ownerWord[12:])

	tokenTopic, err := hex.DecodeString(strings.TrimPrefix(log.Topics[2], "0x"))
	if err != nil {
		return out, fmt.Errorf("decode tokenId topic: %w", err)
	}
	tokenWord, ok := word(tokenTopic, 0)
	if !ok {
		return out, fmt.Errorf("missing tokenId topic")
	}
	out.TokenID = new(big.Int).SetBytes(tokenWord).Uint64()

	usernameOff, ok := wordUint(data, 0)
	if !ok {
		return out, fmt.Errorf("missing githubUsername offset")
	}
	avatarID, ok := wordUint(data, 1)
	if !ok {
		return out, fmt.Errorf("missing avatarId")
	}
	score, ok := wordUint(data, 2)
	if !ok {
		return out, fmt.Errorf("missing skillScore")
	}

	username, err := decodeString(data, int(usernameOff.Int64()))
	if err != nil {
		return out, err
	}

	out.GithubUsername = username
	out.AvatarID = avatarID.Uint64()
	out.SkillScore = int(score.Int64())
	return out, nil
}
