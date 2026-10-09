package solana

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// Commitment levels.
const (
	Processed = "processed"
	Confirmed = "confirmed"
	Finalized = "finalized"
)

// RPC is a JSON-RPC client for one Solana node. It only reads, and
// simulates: nothing in it submits a transaction. Algebra never sends one
// itself; for x402 the sponsor does.
type RPC struct {
	url     string
	hc      *http.Client
	maxBody int64
	id      atomic.Int64
}

// NewRPC builds a client for an endpoint. A nil client gets sane timeouts.
func NewRPC(url string, hc *http.Client) *RPC {
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	return &RPC{url: url, hc: hc, maxBody: 8 << 20}
}

// RPCError is a node's error response.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("solana rpc error %d: %s", e.Code, e.Message) }

// ErrAccountNotFound: the account doesn't exist.
var ErrAccountNotFound = errors.New("solana: account not found")

func (r *RPC) call(ctx context.Context, method string, params []any, out any) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": r.id.Add(1), "method": method, "params": params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.hc.Do(req)
	if err != nil {
		return fmt.Errorf("solana rpc %s: %w", method, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, r.maxBody+1))
	if err != nil {
		return fmt.Errorf("solana rpc %s: reading response: %w", method, err)
	}
	if int64(len(raw)) > r.maxBody {
		return fmt.Errorf("solana rpc %s: response is larger than %d bytes", method, r.maxBody)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("solana rpc %s: rate limited by the node", method)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("solana rpc %s: node answered %d", method, resp.StatusCode)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *RPCError       `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("solana rpc %s: unreadable response: %w", method, err)
	}
	if env.Error != nil {
		return env.Error
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("solana rpc %s: unexpected result: %w", method, err)
	}
	return nil
}

func commit(c string) map[string]any { return map[string]any{"commitment": c} }

// GetGenesisHash identifies the cluster the node serves.
func (r *RPC) GetGenesisHash(ctx context.Context) (string, error) {
	var out string
	return out, r.call(ctx, "getGenesisHash", nil, &out)
}

// BlockhashInfo is a recent blockhash and the last block height at which a
// transaction using it can still land.
type BlockhashInfo struct {
	Blockhash            [32]byte
	LastValidBlockHeight uint64
}

// GetLatestBlockhash returns a blockhash to build a transaction on.
func (r *RPC) GetLatestBlockhash(ctx context.Context, commitment string) (BlockhashInfo, error) {
	var out struct {
		Value struct {
			Blockhash            string `json:"blockhash"`
			LastValidBlockHeight uint64 `json:"lastValidBlockHeight"`
		} `json:"value"`
	}
	if err := r.call(ctx, "getLatestBlockhash", []any{commit(commitment)}, &out); err != nil {
		return BlockhashInfo{}, err
	}
	b, err := DecodeBase58(out.Value.Blockhash)
	if err != nil || len(b) != 32 {
		return BlockhashInfo{}, errors.New("solana: the node returned a malformed blockhash")
	}
	info := BlockhashInfo{LastValidBlockHeight: out.Value.LastValidBlockHeight}
	copy(info.Blockhash[:], b)
	return info, nil
}

// GetBlockHeight is the node's current block height.
func (r *RPC) GetBlockHeight(ctx context.Context, commitment string) (uint64, error) {
	var out uint64
	return out, r.call(ctx, "getBlockHeight", []any{commit(commitment)}, &out)
}

// AccountInfo is the part of an account Algebra reads.
type AccountInfo struct {
	Lamports   uint64
	Owner      PublicKey
	Data       []byte
	Executable bool
}

// GetAccountInfo returns an account, or nil when it doesn't exist.
func (r *RPC) GetAccountInfo(ctx context.Context, pk PublicKey, commitment string) (*AccountInfo, error) {
	var out struct {
		Value *struct {
			Lamports   uint64   `json:"lamports"`
			Owner      string   `json:"owner"`
			Data       []string `json:"data"`
			Executable bool     `json:"executable"`
		} `json:"value"`
	}
	params := []any{pk.String(), map[string]any{"encoding": "base64", "commitment": commitment}}
	if err := r.call(ctx, "getAccountInfo", params, &out); err != nil {
		return nil, err
	}
	if out.Value == nil {
		return nil, nil
	}
	owner, err := ParsePublicKey(out.Value.Owner)
	if err != nil {
		return nil, err
	}
	info := &AccountInfo{Lamports: out.Value.Lamports, Owner: owner, Executable: out.Value.Executable}
	if len(out.Value.Data) > 0 {
		if info.Data, err = base64.StdEncoding.DecodeString(out.Value.Data[0]); err != nil {
			return nil, errors.New("solana: the node returned malformed account data")
		}
	}
	return info, nil
}

// GetBalance is an account's lamports.
func (r *RPC) GetBalance(ctx context.Context, pk PublicKey, commitment string) (uint64, error) {
	var out struct {
		Value uint64 `json:"value"`
	}
	err := r.call(ctx, "getBalance", []any{pk.String(), commit(commitment)}, &out)
	return out.Value, err
}

// TokenBalance is a token account's balance in the mint's minor units.
type TokenBalance struct {
	Amount   uint64
	Decimals uint8
}

// GetTokenAccountBalance reads a token account; ErrAccountNotFound if it
// doesn't exist.
func (r *RPC) GetTokenAccountBalance(ctx context.Context, pk PublicKey, commitment string) (TokenBalance, error) {
	var out struct {
		Value struct {
			Amount   string `json:"amount"`
			Decimals uint8  `json:"decimals"`
		} `json:"value"`
	}
	if err := r.call(ctx, "getTokenAccountBalance", []any{pk.String(), commit(commitment)}, &out); err != nil {
		var re *RPCError
		if errors.As(err, &re) && re.Code == -32602 {
			return TokenBalance{}, ErrAccountNotFound
		}
		return TokenBalance{}, err
	}
	n, err := strconv.ParseUint(out.Value.Amount, 10, 64)
	if err != nil {
		return TokenBalance{}, errors.New("solana: the node returned a malformed token amount")
	}
	return TokenBalance{Amount: n, Decimals: out.Value.Decimals}, nil
}

// SignatureInfo is one entry of an address's transaction history.
type SignatureInfo struct {
	Signature string `json:"signature"`
	Slot      uint64 `json:"slot"`
	// Err is non-nil when the transaction landed and failed.
	Err       any    `json:"err"`
	BlockTime *int64 `json:"blockTime"`
}

// GetSignaturesForAddress lists recent transactions that touched an address,
// newest first.
func (r *RPC) GetSignaturesForAddress(ctx context.Context, pk PublicKey, limit int, before, commitment string) ([]SignatureInfo, error) {
	opts := map[string]any{"limit": limit, "commitment": commitment}
	if before != "" {
		opts["before"] = before
	}
	var out []SignatureInfo
	err := r.call(ctx, "getSignaturesForAddress", []any{pk.String(), opts}, &out)
	return out, err
}

// ParsedInstruction is an instruction as the node decodes it.
type ParsedInstruction struct {
	Program   string          `json:"program"`
	ProgramID string          `json:"programId"`
	Parsed    json.RawMessage `json:"parsed"`
	Accounts  []string        `json:"accounts"`
	Data      string          `json:"data"`
}

// TxInfo is a confirmed transaction as the node decodes it.
type TxInfo struct {
	Slot       uint64
	BlockTime  *int64
	Err        any
	Signatures []string
	// Instructions are the top-level instructions.
	Instructions []ParsedInstruction
}

// GetTransaction fetches a transaction, or nil when the node doesn't have it
// at that commitment.
func (r *RPC) GetTransaction(ctx context.Context, signature, commitment string) (*TxInfo, error) {
	var out *struct {
		Slot      uint64 `json:"slot"`
		BlockTime *int64 `json:"blockTime"`
		Meta      *struct {
			Err any `json:"err"`
		} `json:"meta"`
		Transaction struct {
			Signatures []string `json:"signatures"`
			Message    struct {
				Instructions []ParsedInstruction `json:"instructions"`
			} `json:"message"`
		} `json:"transaction"`
	}
	// A transaction newer than the version asked for is an error, not a skip,
	// and other people's transactions share the accounts Algebra scans. Ask
	// for every version; all that is read from one is its signatures and
	// instructions.
	params := []any{signature, map[string]any{"encoding": "jsonParsed", "commitment": commitment, "maxSupportedTransactionVersion": 255}}
	if err := r.call(ctx, "getTransaction", params, &out); err != nil {
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	info := &TxInfo{Slot: out.Slot, BlockTime: out.BlockTime, Signatures: out.Transaction.Signatures, Instructions: out.Transaction.Message.Instructions}
	if out.Meta != nil {
		info.Err = out.Meta.Err
	}
	return info, nil
}

// Memo returns the text of the transaction's memo instruction, if it has one.
func (t *TxInfo) Memo() (string, bool) {
	for _, in := range t.Instructions {
		if in.Program == "spl-memo" || in.ProgramID == MemoProgram.String() {
			var s string
			if json.Unmarshal(in.Parsed, &s) == nil {
				return s, true
			}
		}
	}
	return "", false
}

// TokenTransfer is a decoded SPL token transferChecked.
type TokenTransfer struct {
	Source, Destination, Mint, Authority string
	Amount                               uint64
	Decimals                             uint8
}

// Transfers returns the transaction's top-level SPL token transferChecked
// instructions.
func (t *TxInfo) Transfers() []TokenTransfer {
	var out []TokenTransfer
	for _, in := range t.Instructions {
		if in.Program != "spl-token" {
			continue
		}
		var p struct {
			Type string `json:"type"`
			Info struct {
				Source      string `json:"source"`
				Destination string `json:"destination"`
				Mint        string `json:"mint"`
				Authority   string `json:"authority"`
				TokenAmount struct {
					Amount   string `json:"amount"`
					Decimals uint8  `json:"decimals"`
				} `json:"tokenAmount"`
			} `json:"info"`
		}
		if json.Unmarshal(in.Parsed, &p) != nil || p.Type != "transferChecked" {
			continue
		}
		amount, err := strconv.ParseUint(p.Info.TokenAmount.Amount, 10, 64)
		if err != nil {
			continue
		}
		out = append(out, TokenTransfer{
			Source: p.Info.Source, Destination: p.Info.Destination, Mint: p.Info.Mint, Authority: p.Info.Authority,
			Amount: amount, Decimals: p.Info.TokenAmount.Decimals,
		})
	}
	return out
}

// Simulation is the result of running a transaction without sending it.
type Simulation struct {
	Err           any      `json:"err"`
	Logs          []string `json:"logs"`
	UnitsConsumed uint64   `json:"unitsConsumed"`
}

// SimulateTransaction runs a transaction against the node's current state
// without sending it. Signatures aren't verified, so a partially signed
// transaction can be checked; the blockhash is replaced with a current one so
// a transaction built a while ago still runs.
func (r *RPC) SimulateTransaction(ctx context.Context, txBase64, commitment string) (*Simulation, error) {
	var out struct {
		Value Simulation `json:"value"`
	}
	opts := map[string]any{"encoding": "base64", "sigVerify": false, "replaceRecentBlockhash": true, "commitment": commitment}
	if err := r.call(ctx, "simulateTransaction", []any{txBase64, opts}, &out); err != nil {
		return nil, err
	}
	return &out.Value, nil
}
