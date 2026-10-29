package wiring

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/platform/config"
	"github.com/project-algebra/algebra/internal/platform/postgres"
	"github.com/project-algebra/algebra/internal/platform/safehttp"
	"github.com/project-algebra/algebra/providers/keyed"
)

// buildKeyedTools lets a person's agents use web tools with their own API keys
// (providers/keyed). The keys are sealed under a key derived from the master
// key and used for nothing else. The vendor bills the person, so the rail
// moves no money; the runner is the one place a key is ever read.
//
// web is the operator's own keys from the environment (TINY_FISH_API, ...), for
// the accounts ALGEBRA_OPERATOR_EMAILS names (see useOperatorKeys).
func buildKeyedTools(db *postgres.DB, masterKey []byte, econSvc *app.EconomicService, exec *app.ExecutionService, passes app.PassOwners, modes app.AccountModes, web config.WebToolsConfig, users app.OperatorUsers) (*app.ToolKeys, error) {
	mac := hmac.New(sha256.New, masterKey)
	mac.Write([]byte("algebra:tool-keys:v1"))
	sealer, err := privacy.NewAESGCMEncryptor(mac.Sum(nil))
	if err != nil {
		return nil, fmt.Errorf("wiring: tool key sealing: %w", err)
	}
	tools := keyed.NewTools(nil)
	var vendors []app.ToolVendor
	for _, v := range tools.Vendors() {
		vendors = append(vendors, app.ToolVendor{ID: v.ID, Name: v.Name, Site: v.Site, Tools: v.Tools})
	}
	keys := app.NewToolKeys(postgres.NewToolKeyRepo(db), sealer, vendors, tools.Candidates, modes)
	if err := useOperatorKeys(keys, web, users); err != nil {
		return nil, err
	}
	econSvc.RegisterRail(keyed.NewRail())
	exec.RegisterRunner(keyed.NewRunner(keyed.Config{
		// An agent run can take a minute and a half, so the client waits longer than for a price.
		HTTP: safehttp.New(safehttp.Options{Timeout: 110 * time.Second, MaxBody: 4 << 20}),
		Keys: keys, Passes: passes, Tools: tools,
	}))
	return keys, nil
}

// useOperatorKeys hands the service the vendor keys the operator keeps in the
// environment, for the accounts at the operator's addresses. A key is the
// operator's own account with the vendor, so with keys but no address no
// account can use them, and the log says so (by vendor, never the key).
func useOperatorKeys(keys *app.ToolKeys, web config.WebToolsConfig, users app.OperatorUsers) error {
	if len(web.Keys) == 0 {
		return nil
	}
	emails := make([]string, 0, len(web.OperatorEmails))
	for _, e := range web.OperatorEmails {
		n, err := account.NormalizeEmail(e)
		if err != nil {
			return fmt.Errorf("wiring: ALGEBRA_OPERATOR_EMAILS has an entry that is not an email address: %q", e)
		}
		emails = append(emails, n)
	}
	if err := keys.SetOperatorKeys(web.Keys, app.NewOperatorEmails(emails, users)); err != nil {
		return fmt.Errorf("wiring: %w", err)
	}
	vendors := make([]string, 0, len(web.Keys))
	for v := range web.Keys {
		vendors = append(vendors, v)
	}
	sort.Strings(vendors)
	if len(emails) == 0 {
		slog.Default().Warn("web tool keys are set in the environment but ALGEBRA_OPERATOR_EMAILS is not: no account can use them", "vendors", vendors)
	} else {
		slog.Default().Info("web tool keys from the environment are on for the operator's accounts", "vendors", vendors, "operators", len(emails))
	}
	return nil
}
