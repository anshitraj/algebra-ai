package wiring

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/platform/config"
	"github.com/project-algebra/algebra/internal/platform/evm"
	"github.com/project-algebra/algebra/internal/platform/payersigner"
	"github.com/project-algebra/algebra/internal/platform/postgres"
	"github.com/project-algebra/algebra/providers/evmx402"
	"github.com/project-algebra/algebra/providers/solanax402"
)

// evmReadyMinor is what the EVM payer must hold on a chain for the router to
// offer it: 0.01 USDC, ten cheap calls.
const evmReadyMinor = 10_000

// buildEVMRails makes the Base Sepolia and Arc testnet payment rails, one per
// chain with a node, all paying from the same EVM payer key. None when no EVM
// payer is configured.
func buildEVMRails(ctx context.Context, cfg *config.Config, db *postgres.DB, masterKey []byte) ([]*evmx402.Rail, error) {
	if !cfg.EVMPayer.On() {
		return nil, nil
	}
	var deps payersigner.Deps
	if cfg.EVMPayer.Generate {
		if len(masterKey) == 0 {
			return nil, errors.New("EVM payer: a generated key needs ALGEBRA_MASTER_KEY")
		}
		enc, err := payersigner.NewEncryptor(masterKey)
		if err != nil {
			return nil, fmt.Errorf("EVM payer: %w", err)
		}
		deps = payersigner.Deps{Store: postgres.NewSignerRepo(db), Enc: enc}
	}
	sig, err := payersigner.ResolveEVM(ctx, payersigner.EVMSource{
		Cluster: payersigner.EVMTestnetCluster, Key: cfg.EVMPayer.Key, Generate: cfg.EVMPayer.Generate, ExpectAddress: cfg.EVMPayer.Address,
	}, deps)
	if err != nil {
		return nil, err
	}
	if sig.Created {
		log.Printf("wiring: GENERATED the EVM testnet payer %s and stored it sealed in the database. Fund it with Base Sepolia USDC and a little "+
			"Base Sepolia ETH, and with Arc testnet USDC (https://faucet.circle.com), and pin it with EVM_TESTNET_PAYER_ADDRESS=%s", sig.Key.Hex(), sig.Key.Hex())
	}
	var rails []*evmx402.Rail
	for _, network := range []string{chain.BaseSepolia, chain.ArcTestnet} {
		url := cfg.Vault.EVMRPC[network]
		if url == "" {
			continue
		}
		rail, err := evmx402.New(evmx402.Config{Network: network, RPC: evm.NewRPC(url), Key: sig.Key})
		if err != nil {
			return nil, err
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err = rail.Status(cctx)
		cancel()
		switch {
		case errors.Is(err, evmx402.ErrWrongChain):
			return nil, fmt.Errorf("%s: %w", network, err)
		case err != nil:
			log.Printf("wiring: the %s node didn't answer (%v); its rail runs, and is offered once its payer is seen funded", network, err)
		}
		rails = append(rails, rail)
	}
	if len(rails) > 0 {
		log.Printf("wiring: EVM x402 rails on %v, paying from %s", evmNetworks(rails), sig.Key.Hex())
	}
	RefreshEVMReadiness(ctx, rails)
	return rails, nil
}

// buildEVMAllowances lets agents spend from owners' vaults on the EVM chains
// with a rail, through USDC permits to the rail's payer. Nil with no EVM rail.
func buildEVMAllowances(cfg *config.Config, rails []*evmx402.Rail, db *postgres.DB, passes app.PassOwners, reservations app.ReservationReader, limiter app.RateLimiter) *app.EVMAllowanceService {
	var nets []*app.EVMNetwork
	for _, r := range rails {
		nets = append(nets, &app.EVMNetwork{Network: r.Network(), RailName: r.Name(), RPC: r.RPC(), Payer: r.Key(), Token: r.Token(), Domain: r.Domain(), Rail: r})
	}
	if len(nets) == 0 {
		return nil
	}
	log.Printf("wiring: vault allowances on %v: permits to %s", evmNetworks(rails), rails[0].Address())
	return app.NewEVMAllowanceService(postgres.NewVaultAllowanceRepo(db), postgres.NewOwnerVaultRepo(db), passes,
		postgres.NewOnchainPassRepo(db), reservations, nets, cfg.Vault.AllowanceMaxMinor, limiter)
}

// RefreshEVMReadiness offers each EVM chain to the router while its payer
// holds enough USDC, and takes it away when it doesn't, so an empty wallet
// never turns into routed payments that can only fail. Payments already made
// there are settled and reconciled either way.
func RefreshEVMReadiness(ctx context.Context, rails []*evmx402.Rail) {
	for _, r := range rails {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		st, err := r.Status(cctx)
		cancel()
		ready := err == nil && st.USDCMinor >= evmReadyMinor
		if was := chain.Payable(r.Network()); was != ready {
			switch {
			case ready:
				log.Printf("wiring: %s is payable: the payer %s holds %s USDC", r.Network(), r.Address(), chain.FormatUnits(st.USDCMinor, chain.USDCDecimals))
			case err != nil:
				log.Printf("wiring: %s is not offered for now: %v", r.Network(), err)
			default:
				log.Printf("wiring: %s is not offered: the payer %s holds %s USDC (it needs %s)", r.Network(), r.Address(),
					chain.FormatUnits(st.USDCMinor, chain.USDCDecimals), chain.FormatUnits(evmReadyMinor, chain.USDCDecimals))
			}
		}
		chain.SetPayable(r.Network(), ready)
	}
}

func evmNetworks(rails []*evmx402.Rail) []string {
	out := make([]string, len(rails))
	for i, r := range rails {
		out[i] = r.Network()
	}
	return out
}

// testRailNames are the rails that pay with test money: Solana devnet's and
// every EVM test chain's.
func testRailNames(rails []*solanax402.Rail, evmRails []*evmx402.Rail) []string {
	var names []string
	for _, r := range rails {
		if r.Network() == chain.SolanaDevnet {
			names = append(names, r.Name())
		}
	}
	for _, r := range evmRails {
		if chain.IsTestNetwork(r.Network()) {
			names = append(names, r.Name())
		}
	}
	return names
}
