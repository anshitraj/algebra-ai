package payersigner

import (
	"context"
	"strings"
	"testing"
)

func TestEVMGeneratedSignerSurvivesARestartAndIsTestnetOnly(t *testing.T) {
	ctx := context.Background()
	st := newMemStore()
	d := deps(t, st, 1)
	first, err := ResolveEVM(ctx, EVMSource{Cluster: EVMTestnetCluster, Generate: true}, d)
	if err != nil || !first.Created || !strings.HasPrefix(first.Key.Hex(), "0x") {
		t.Fatalf("first start: %+v %v", first, err)
	}
	again, err := ResolveEVM(ctx, EVMSource{Cluster: EVMTestnetCluster, Generate: true}, d)
	if err != nil || again.Created || again.Key.Hex() != first.Key.Hex() {
		t.Fatalf("a restart opens the same key: %+v %v", again, err)
	}
	if _, err := ResolveEVM(ctx, EVMSource{Cluster: EVMTestnetCluster, Generate: true}, deps(t, st, 2)); err == nil {
		t.Error("a wrong master key never makes a new signer")
	}
	if _, err := ResolveEVM(ctx, EVMSource{Cluster: "evm-mainnet", Generate: true}, d); err == nil {
		t.Error("real money never gets a generated key")
	}
	if _, err := ResolveEVM(ctx, EVMSource{Cluster: EVMTestnetCluster, Generate: true, ExpectAddress: "0x0000000000000000000000000000000000000001"}, d); err == nil {
		t.Error("a pin that doesn't match stops the start")
	}
	env, err := ResolveEVM(ctx, EVMSource{Cluster: "evm-mainnet", Key: "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"}, d)
	if err != nil || env.Key.Hex() != "0x2c7536e3605d9c16a7a3d7b1898e529396a65c23" || env.Mode != ModeEnv {
		t.Fatalf("an operator's key: %+v %v", env, err)
	}
}
