package v1

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/account"
)

// faucetStatus: GET /api/v1/me/faucet. Whether this server has a devnet
// faucet, what one drop is, and the wallet of the person's own that a drop
// would go to (the one they signed in with, else the one Privy made).
func (a *API) faucetStatus(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	out := struct {
		app.FaucetStatus
		// Wallet is where a drop would go.
		Wallet string `json:"wallet,omitempty"`
	}{FaucetStatus: a.b.DevnetFaucet.Status()}
	if wallet, err := a.faucetWallet(r, sess.UserID); err == nil {
		out.Wallet = wallet
	}
	writeJSON(w, http.StatusOK, out)
}

// faucetDrop: POST /api/v1/me/faucet {"address": "..."}. Tops up one of the
// person's own wallets with devnet SOL and USDC (address omitted: the default
// wallet). Only what the wallet lacks is sent, at most once a day.
func (a *API) faucetDrop(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	if a.b.DevnetFaucet == nil {
		writeJSON(w, http.StatusNotImplemented, errorBody{Error: app.ErrFaucetOff.Error() + ": get devnet SOL at faucet.solana.com and USDC at faucet.circle.com"})
		return
	}
	if a.b.Accounts != nil {
		if mode, err := a.b.Accounts.UserMode(r.Context(), sess.UserID); err == nil && mode == account.ModeDemo {
			writeJSON(w, http.StatusForbidden, errorBody{Error: "demo accounts pay with simulated USDC and have no wallet; sign in with an email or a wallet to get devnet funds"})
			return
		}
	}
	var req struct {
		Address string `json:"address"`
	}
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, err)
			return
		}
	}
	address := req.Address
	if address == "" {
		var err error
		if address, err = a.faucetWallet(r, sess.UserID); err != nil {
			writeJSON(w, http.StatusConflict, errorBody{Error: "your account has no Solana wallet yet: sign in with a wallet or an email to get one"})
			return
		}
	}
	drop, err := a.b.DevnetFaucet.Drop(r.Context(), sess.UserID, address, clientMeta(r).IP)
	if err != nil {
		var limit *app.FaucetLimitError
		var enough *app.FaucetNotNeeded
		switch {
		case errors.As(err, &limit):
			secs := int64(limit.RetryAfter.Seconds())
			if secs < 1 {
				secs = 1
			}
			w.Header().Set("Retry-After", strconv.FormatInt(secs, 10))
			writeJSON(w, http.StatusTooManyRequests, errorBody{Error: limit.Error()})
		case errors.As(err, &enough):
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": enough.Error(), "already_funded": true, "sol_lamports": enough.SOLLamports, "usdc_minor": enough.USDCMinor,
			})
		case errors.Is(err, app.ErrFaucetEmpty), errors.Is(err, app.ErrFaucetUnavailable):
			writeJSON(w, http.StatusServiceUnavailable, errorBody{Error: err.Error()})
		case errors.Is(err, app.ErrInvalidRequest):
			writeJSON(w, http.StatusBadRequest, errorBody{Error: strings.TrimPrefix(err.Error(), app.ErrInvalidRequest.Error()+": ")})
		default:
			writeJSON(w, http.StatusBadGateway, errorBody{Error: "the devnet network didn't complete the transfer: " + err.Error()})
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"drop": drop})
}

// faucetWallet is the Solana wallet a drop goes to when none is named: the
// person's own (external) wallet if they signed in with one, else the one
// Privy made for them.
func (a *API) faucetWallet(r *http.Request, userID string) (string, error) {
	if a.b.Accounts == nil {
		return "", errors.New("no accounts")
	}
	owned, err := a.b.Accounts.Wallets(r.Context(), userID)
	if err != nil {
		return "", err
	}
	first := ""
	for _, w := range owned {
		if w.Chain != account.ChainSolana {
			continue
		}
		if w.Kind == account.WalletExternal {
			return w.Address, nil
		}
		if first == "" {
			first = w.Address
		}
	}
	if first == "" {
		return "", errors.New("no wallet")
	}
	return first, nil
}
