package wiring

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/platform/config"
	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/internal/platform/solana/solanatest"
	"github.com/project-algebra/algebra/providers/solanax402"
)

func TestParseConfiguredProviders(t *testing.T) {
	got, err := ParseConfiguredProviders(`[
		{"capability":"solana.token-risk","provider":"Acme","endpoint":"https://API.acme.example/risk/","method":"post","network":"solana"},
		{"capability":"wallet.analytics","provider":"acme","endpoint":"https://api.acme.example/wallet","network":"solana"},
		{"capability":"solana.token-risk","provider":"other","name":"Other Co","endpoint":"https://other.example/risk","network":"base"}
	]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got["acme"]) != 2 || len(got["other"]) != 1 {
		t.Fatalf("grouped by provider: %+v", got)
	}
	c := got["acme"][0]
	if c.Provider != "acme" || c.Endpoint != "https://api.acme.example/risk" || c.Method != "POST" || c.Network != "solana" || c.ExecutionType != routing.ExecX402 {
		t.Errorf("normalised: %+v", c)
	}
	if c.Trust() != routing.TrustNative {
		t.Errorf("an operator-pinned provider is trusted as native, got %s", c.Trust())
	}
	if got["other"][0].Name != "Other Co" {
		t.Errorf("name: %+v", got["other"][0])
	}
	if empty, err := ParseConfiguredProviders("  "); err != nil || len(empty) != 0 {
		t.Errorf("unset means none: %v %v", empty, err)
	}
}

func TestParseConfiguredProvidersFailsLoudly(t *testing.T) {
	for name, raw := range map[string]string{
		"not json":        `nope`,
		"not an array":    `{"provider":"a"}`,
		"unknown field":   `[{"capability":"a.b","provider":"p","endpoint":"https://x.example/","network":"solana","trusted":true}]`,
		"plain http":      `[{"capability":"a.b","provider":"p","endpoint":"http://x.example/","network":"solana"}]`,
		"private address": `[{"capability":"a.b","provider":"p","endpoint":"https://10.0.0.5/","network":"solana"}]`,
		"no endpoint":     `[{"capability":"a.b","provider":"p","network":"solana"}]`,
		"bad capability":  `[{"capability":"X","provider":"p","endpoint":"https://x.example/","network":"solana"}]`,
		"duplicate":       `[{"capability":"a.b","provider":"p","endpoint":"https://x.example/","network":"solana"},{"capability":"a.b","provider":"p","endpoint":"https://x.example","network":"solana"}]`,
	} {
		if _, err := ParseConfiguredProviders(raw); err == nil {
			t.Errorf("%s: a mistake in ECONOMIC_PROVIDERS must stop startup, not drop the provider", name)
		}
	}
	if _, err := ParseConfiguredProviders(`[{"capability":"a.b","provider":"p","endpoint":"http://x.example/","network":"solana"}]`); err == nil || !strings.Contains(err.Error(), "entry 1 (p)") {
		t.Errorf("the error names the entry: %v", err)
	}
}

func TestListenPort(t *testing.T) {
	for addr, want := range map[string]int{":8080": 8080, "0.0.0.0:9000": 9000, "127.0.0.1:1": 1, "[::1]:8443": 8443, "8080": 0, "": 0, ":0": 0, ":70000": 0, ":http": 0} {
		if got := listenPort(addr); got != want {
			t.Errorf("listenPort(%q) = %d, want %d", addr, got, want)
		}
	}
}

func writeKeypair(t *testing.T) (path, text string) {
	t.Helper()
	kp, err := solana.NewKeypair()
	if err != nil {
		t.Fatal(err)
	}
	// The same JSON array `solana-keygen` writes.
	text = kp.ExportKeygenJSON()
	path = filepath.Join(t.TempDir(), "id.json")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, text
}

func TestBuildSolanaRail(t *testing.T) {
	ctx := context.Background()
	path, _ := writeKeypair(t)

	// A good setup: the node serves the configured cluster.
	devnet := solanatest.New(solana.DevnetGenesisHash)
	rail, err := buildSolanaRail(ctx, config.SolanaConfig{Cluster: "devnet", RPCURL: devnet.Serve(t), KeypairFile: path, MaxPaymentMinor: 250_000})
	if err != nil || rail == nil || rail.Network() != "solana-devnet" || rail.Name() != solanax402.DevnetRailName {
		t.Fatalf("a good setup: %v %v", rail, err)
	}

	// A node on another cluster is a configuration mistake and stops startup.
	mainnet := solanatest.New(solana.MainnetGenesisHash)
	if _, err := buildSolanaRail(ctx, config.SolanaConfig{Cluster: "devnet", RPCURL: mainnet.Serve(t), KeypairFile: path}); !errors.Is(err, solanax402.ErrWrongCluster) {
		t.Errorf("paying devnet through a mainnet node (or the reverse) must be fatal: %v", err)
	}

	// A node that is merely down leaves the rail out; it must not take the API with it.
	down := solanatest.New(solana.DevnetGenesisHash)
	url := down.Serve(t)
	down.FailMethod["getGenesisHash"] = errors.New("node is down")
	if rail, err := buildSolanaRail(ctx, config.SolanaConfig{Cluster: "devnet", RPCURL: url, KeypairFile: path}); err != nil || rail != nil {
		t.Errorf("an unverifiable cluster: no rail, no error: %v %v", rail, err)
	}
}

func TestBuildSolanaRailRefusesAnUnusableWalletWithoutEchoingIt(t *testing.T) {
	ctx := context.Background()
	devnet := solanatest.New(solana.DevnetGenesisHash)
	url := devnet.Serve(t)

	secretish := "this-is-not-a-key-but-looks-private-9f8a7b6c5d4e3f2a1b"
	_, err := buildSolanaRail(ctx, config.SolanaConfig{Cluster: "devnet", RPCURL: url, Keypair: secretish})
	if err == nil {
		t.Fatal("a bad wallet must stop startup")
	}
	if strings.Contains(err.Error(), secretish) {
		t.Errorf("the error must not echo the key text: %v", err)
	}
	if _, err := buildSolanaRail(ctx, config.SolanaConfig{Cluster: "devnet", RPCURL: url, KeypairFile: filepath.Join(t.TempDir(), "missing.json")}); err == nil {
		t.Error("a missing key file must stop startup")
	}
	// The inline form works too.
	_, text := writeKeypair(t)
	if rail, err := buildSolanaRail(ctx, config.SolanaConfig{Cluster: "devnet", RPCURL: url, Keypair: text}); err != nil || rail == nil {
		t.Errorf("inline keypair: %v %v", rail, err)
	}
}
