package wiring

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/platform/postgres"
	"github.com/project-algebra/algebra/internal/platform/safehttp"
	"github.com/project-algebra/algebra/providers/keyed"
)

// buildKeyedTools lets a person's agents use web tools with their own API keys
// (providers/keyed). The keys are sealed under a key derived from the master
// key and used for nothing else. The vendor bills the person, so the rail
// moves no money; the runner is the one place a key is ever read.
func buildKeyedTools(db *postgres.DB, masterKey []byte, econSvc *app.EconomicService, exec *app.ExecutionService, passes app.PassOwners, modes app.AccountModes) (*app.ToolKeys, error) {
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
	econSvc.RegisterRail(keyed.NewRail())
	exec.RegisterRunner(keyed.NewRunner(keyed.Config{
		// An agent run can take a minute and a half, so the client waits longer than for a price.
		HTTP: safehttp.New(safehttp.Options{Timeout: 110 * time.Second, MaxBody: 4 << 20}),
		Keys: keys, Passes: passes, Tools: tools,
	}))
	return keys, nil
}
