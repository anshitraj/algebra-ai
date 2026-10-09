// Command merchant-login links a merchant account to Algebra, for merchants
// that publish an official OAuth-protected MCP server (Zepto, Swiggy
// Instamart).
//
// It prints the merchant's authorization URL. You open it in your own
// browser and sign in on the merchant's own page (phone number + OTP).
// Algebra never sees or relays the OTP: it only receives the OAuth code on a
// loopback redirect, exchanges it, and stores the resulting tokens encrypted
// with ALGEBRA_MASTER_KEY under MERCHANT_SESSION_DIR.
//
// It then lists the live MCP tools and writes their manifest (tool names and
// JSON schemas — no account data) to -manifest-dir, so a merchant's actual
// tool contract can be reviewed before any mapping is written against it.
//
// Usage:
//
//	go run ./cmd/merchant-login -merchant zepto
//	go run ./cmd/merchant-login -merchant swiggy_instamart -alias shipping:home
//	go run ./cmd/merchant-login -merchant zepto -unlink
//	go run ./cmd/merchant-login -merchant swiggy_instamart -address 2   # pick a saved address non-interactively (re-uses an existing link)
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/project-algebra/algebra/connectors/remotemcp"
	"github.com/project-algebra/algebra/connectors/swiggyinstamart"
	"github.com/project-algebra/algebra/connectors/zepto"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/platform/config"
	"github.com/project-algebra/algebra/internal/platform/datadir"
)

type options struct {
	merchant    string
	redirect    string
	alias       string
	manifestDir string
	unlink      bool
	timeout     time.Duration
	address     int
}

func main() {
	var opts options
	flag.StringVar(&opts.merchant, "merchant", "", `merchant to link: "zepto" or "swiggy_instamart"`)
	flag.StringVar(&opts.redirect, "redirect", "http://127.0.0.1:8765/callback", "loopback OAuth redirect URL")
	flag.StringVar(&opts.alias, "alias", "shipping:home", "Algebra shipping alias the chosen Swiggy address stands for (swiggy_instamart only)")
	flag.StringVar(&opts.manifestDir, "manifest-dir", datadir.Path("merchant-tools"), "where to write the live tool manifest")
	flag.BoolVar(&opts.unlink, "unlink", false, "delete the stored session instead of linking")
	flag.DurationVar(&opts.timeout, "timeout", 10*time.Minute, "how long to wait for you to finish signing in")
	flag.IntVar(&opts.address, "address", 0, "swiggy_instamart: which saved address (1-based) the alias delivers to; with an existing link, skips signing in again")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, opts, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "merchant-login:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, opts options, in io.Reader, out io.Writer) error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	key, err := cfg.MasterKey()
	if err != nil {
		return err
	}
	enc, err := privacy.NewAESGCMEncryptor(key)
	if err != nil {
		return err
	}
	merchants := cfg.Merchants.WithDefaults()
	store := remotemcp.NewFileSessionStore(merchants.SessionDir, enc)

	var endpoint string
	switch opts.merchant {
	case zepto.Name:
		endpoint = merchants.ZeptoMCPEndpoint
	case swiggyinstamart.Name:
		endpoint = merchants.SwiggyInstamartMCPEndpoint
	default:
		return fmt.Errorf("-merchant must be %q or %q (Amazon and Flipkart use API credentials in .env; Blinkit has no official integration to link)", zepto.Name, swiggyinstamart.Name)
	}

	if opts.unlink {
		if err := store.Delete(opts.merchant); err != nil {
			return err
		}
		fmt.Fprintf(out, "Unlinked %s. (To revoke Algebra's access on the merchant side too, sign out of connected apps in your %s account.)\n", opts.merchant, opts.merchant)
		return nil
	}

	// Picking an address for an account that's already linked needs no new
	// sign-in — just the saved session.
	if opts.merchant == swiggyinstamart.Name && opts.address > 0 {
		if _, err := store.Load(opts.merchant); err == nil {
			client, err := remotemcp.NewClient(remotemcp.Config{Merchant: opts.merchant, Endpoint: endpoint, Store: store})
			if err != nil {
				return err
			}
			defer client.Close()
			return chooseSwiggyAddress(ctx, client, store, normalizeAlias(opts.alias), opts.address, in, out)
		}
	}

	fmt.Fprintf(out, "Linking %s via %s\n", opts.merchant, endpoint)
	sess, err := remotemcp.Login(ctx, opts.merchant, endpoint, remotemcp.LoginOptions{
		RedirectURL: opts.redirect,
		Timeout:     opts.timeout,
		ShowURL: func(authURL string) {
			fmt.Fprintf(out, "\nOpen this URL in your browser and sign in on %s's own page:\n\n  %s\n\nWaiting for the redirect to %s ...\n", opts.merchant, authURL, opts.redirect)
		},
	})
	if err != nil {
		return err
	}
	if err := store.Save(sess); err != nil {
		return err
	}
	fmt.Fprintf(out, "Linked. Session stored encrypted in %s\n", merchants.SessionDir)

	client, err := remotemcp.NewClient(remotemcp.Config{Merchant: opts.merchant, Endpoint: endpoint, Store: store})
	if err != nil {
		return err
	}
	defer client.Close()
	tools, err := client.Tools(ctx)
	if err != nil {
		return fmt.Errorf("linked, but listing the merchant's tools failed: %w", err)
	}
	path, err := writeManifest(opts.manifestDir, opts.merchant, endpoint, tools)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "The live server exposes %d tools; manifest written to %s\n", len(tools), path)

	switch opts.merchant {
	case zepto.Name:
		fmt.Fprintln(out, "\nZepto publishes no schemas for these tools, so Algebra keeps Zepto search, cart and checkout off until the manifest is reviewed and mapped in connectors/zepto.")
	case swiggyinstamart.Name:
		if err := remotemcp.CheckTools(tools, swiggyinstamart.Contract); err != nil {
			fmt.Fprintf(out, "\nWARNING: the live tools differ from Swiggy's published Instamart reference, so the connector will stay disabled: %v\n", err)
		}
		if err := chooseSwiggyAddress(ctx, client, store, normalizeAlias(opts.alias), opts.address, in, out); err != nil {
			return err
		}
		fmt.Fprintln(out, "\nSwiggy Instamart places REAL orders (Cash on Delivery). Swiggy reviews production access to its MCP servers (builders@swiggy.in); enable the connector with ENABLED_MERCHANTS=...,swiggy_instamart once you have it.")
	}
	return nil
}

func normalizeAlias(alias string) string {
	alias = strings.TrimSpace(alias)
	if !strings.HasPrefix(alias, "shipping:") {
		alias = "shipping:" + alias
	}
	return alias
}

// chooseSwiggyAddress shows the linked account's saved addresses in the
// user's own terminal and records which one the alias stands for. Only the
// opaque address ID is stored.
func chooseSwiggyAddress(ctx context.Context, client *remotemcp.Client, store *remotemcp.FileSessionStore, alias string, preset int, in io.Reader, out io.Writer) error {
	addresses, err := swiggyinstamart.SavedAddresses(ctx, client)
	if err != nil {
		return fmt.Errorf("listing your saved Swiggy addresses: %w", err)
	}
	if len(addresses) == 0 {
		return errors.New("your Swiggy account has no saved addresses; add one in the Swiggy app, then re-run merchant-login")
	}
	fmt.Fprintf(out, "\nWhich saved Swiggy address should %q deliver to?\n", alias)
	for i, a := range addresses {
		label := a.Label
		if label == "" {
			label = "Address"
		}
		fmt.Fprintf(out, "  [%d] %s: %s\n", i+1, label, a.AddressLine)
	}
	n := preset
	if n == 0 {
		fmt.Fprint(out, "Number: ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		n, _ = strconv.Atoi(strings.TrimSpace(line))
	}
	if n < 1 || n > len(addresses) {
		return fmt.Errorf("no valid address chosen; the account is linked — pick one with: go run ./cmd/merchant-login -merchant %s -address <number>", swiggyinstamart.Name)
	}

	sess, err := store.Load(swiggyinstamart.Name)
	if err != nil {
		return err
	}
	if sess.Settings == nil {
		sess.Settings = map[string]string{}
	}
	sess.Settings[swiggyinstamart.SettingAddressID] = addresses[n-1].ID
	sess.Settings[swiggyinstamart.SettingShippingAlias] = alias
	if err := store.Save(sess); err != nil {
		return err
	}
	fmt.Fprintf(out, "Saved: %s now delivers to saved address [%d].\n", alias, n)
	return nil
}

type manifest struct {
	Merchant string      `json:"merchant"`
	Endpoint string      `json:"endpoint"`
	ListedAt time.Time   `json:"listed_at"`
	Tools    []*mcp.Tool `json:"tools"`
}

func writeManifest(dir, merchant, endpoint string, tools map[string]*mcp.Tool) (string, error) {
	list := make([]*mcp.Tool, 0, len(tools))
	for _, t := range tools {
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	data, err := json.MarshalIndent(manifest{Merchant: merchant, Endpoint: endpoint, ListedAt: time.Now().UTC(), Tools: list}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding tool manifest: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating manifest dir: %w", err)
	}
	path := filepath.Join(dir, merchant+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("writing tool manifest: %w", err)
	}
	return path, nil
}
