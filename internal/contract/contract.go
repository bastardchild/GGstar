package contract

import (
	"encoding/json"
	"os"
	"strings"
)

// File mirrors config/contract.json (address + ABI + network metadata).
type File struct {
	Network         string            `json:"network"`
	ChainID         int               `json:"chainId"`
	ChainIDHex      string            `json:"chainIdHex"`
	RPCURL          string            `json:"rpcUrl"`
	ExplorerURL     string            `json:"explorerUrl"`
	FaucetURL       string            `json:"faucetUrl"`
	Solidity        string            `json:"solidity"`
	OpenZeppelin    string            `json:"openzeppelin"`
	Deployed        bool              `json:"deployed"`
	ContractAddress string            `json:"contractAddress"`
	DeployTxHash    string            `json:"deployTxHash"`
	ABI             []json.RawMessage `json:"abi"`
}

// Load reads the contract artifact. A missing file is not fatal: the app still
// boots (read-only mode) and the UI shows "not deployed".
func Load(path string) (File, error) {
	var f File

	raw, err := os.ReadFile(path)
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return f, err
	}
	return f, nil
}

// ABIRaw returns the ABI with comments stripped, ready to embed in the HTML page
// as a JS literal.
func (f File) ABIRaw() string {
	if f.ABI == nil {
		return "[]"
	}
	joined := make([]string, 0, len(f.ABI))
	for _, entry := range f.ABI {
		joined = append(joined, string(entry))
	}
	return "[" + strings.Join(joined, ",") + "]"
}
