package app

import (
	"strings"
	"testing"
)

func TestFundingProblem_SaysWhatTheWalletLacksAndWhereToGetIt(t *testing.T) {
	const owner = "9kEVg2noE8uubD6e1jMtEwumULEyNLgr7uYCXDVL4CLW"
	ok := fundingFacts{Cluster: "devnet", Owner: owner, Lamports: 50_000_000, USDCMinor: 2_000_000, DepositMinor: 1_000_000, Simulated: true}
	if got := fundingProblem(ok); got != "" {
		t.Fatalf("a funded wallet and a clean simulation have no problem: %q", got)
	}

	cases := []struct {
		name    string
		change  func(*fundingFacts)
		want    []string
		notWant string
	}{
		{"no USDC", func(f *fundingFacts) { f.USDCMinor = 0 }, []string{"9kEV…4CLW", "holds 0 USDC on Solana devnet", "deposit is 1 USDC", "faucet.circle.com"}, ""},
		{"too little USDC", func(f *fundingFacts) { f.USDCMinor = 250_000 }, []string{"holds 0.25 USDC", "deposit is 1 USDC"}, ""},
		{"no SOL", func(f *fundingFacts) { f.Lamports = 0 }, []string{"holds no SOL on Solana devnet", "faucet.solana.com"}, "USDC"},
		{"SOL short of the rent", func(f *fundingFacts) {
			f.Lamports, f.SimFailed, f.Logs = 1_000_000, true, []string{"Program 11111111111111111111111111111111 invoke [2]", "Transfer: insufficient lamports 1000000, need 2039280"}
		}, []string{"holds 0.001 SOL", "rent", "faucet.solana.com"}, ""},
		{"the program refuses", func(f *fundingFacts) {
			f.SimFailed, f.Logs = true, []string{"Program log: Instruction: CreatePass", "Program log: AnchorError occurred. Error Code: BadTerms. Error Number: 6000."}
		}, []string{"would fail on Solana devnet", "BadTerms"}, "faucet"},
		{"no deposit asked: USDC is not needed", func(f *fundingFacts) { f.USDCMinor, f.DepositMinor = 0, 0 }, nil, ""},
		{"mainnet: no faucet to send anyone to", func(f *fundingFacts) { f.Cluster, f.USDCMinor = "mainnet", 0 }, []string{"on Solana mainnet"}, "faucet"},
		{"the node could not simulate: balances still decide", func(f *fundingFacts) { f.Simulated = false }, nil, ""},
	}
	for _, c := range cases {
		f := ok
		c.change(&f)
		got := fundingProblem(f)
		if len(c.want) == 0 && got != "" {
			t.Errorf("%s: want no problem, got %q", c.name, got)
		}
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q does not say %q", c.name, got, w)
			}
		}
		if c.notWant != "" && strings.Contains(got, c.notWant) {
			t.Errorf("%s: %q should not mention %q", c.name, got, c.notWant)
		}
	}
}
