# Route audit

Every page of the web app, opened in a browser on **2026-10-12** against a
local API (`:8085`, database `algebra_platform`, devnet rail on, four live
catalogs) and the Next.js dev server (`:3400`), signed in as a freshly created
account. "Checked" means the page was loaded and read in the browser, not that
it compiled. Screenshots could not be taken (the browser pane was hidden), so
this is text and DOM evidence: rendered content, console errors, network
failures and layout width.

## How to read it

| Column | Question |
|---|---|
| Renders | Does the page load without an error boundary or a blank screen? |
| Real data | Does what it shows come from the API (or is it labelled as demo data)? |
| Actions | Were its buttons and forms exercised, and did they work? |
| Notes | What a new user would trip on, and anything not checked |

## Public site

| Route | Renders | Real data | Actions | Notes |
|---|---|---|---|---|
| `/` | Yes | Static copy; the dashboard preview is labelled "Example workspace · no real money moves" | Nav and hero links resolve (no empty `href`); no horizontal overflow | Hero actions: Start building free, Book a demo, Explore the demo. Documentation opens GitHub. There is no public provider directory: "Explore providers" needs an account |
| `/pricing` | Yes | Static, mirrors `GET /api/v1/plans` | Links to signup, Book a demo, console | States that paid plans cannot be bought online. No overflow at 375 px |
| `/book-demo` | Yes | Posts to `POST /api/v1/leads` | **Submitted**: an invalid email was refused with a readable message; a valid request was stored (row read back from `demo_requests`) and confirmed | No calendar, and the page says so. No overflow at 375 px |
| `/demo` | Yes | Labelled "INTERACTIVE DEMO · SIMULATED DATA" | Not exercised beyond load | Figures are illustrative and labelled |
| `/verify` | Yes | Calls `POST /api/v1/receipts/verify` | Verify endpoint exercised through the API with the devnet receipt (`valid: true`); the page's button not clicked | |
| `/signup` | Yes | Real | **Account created** with email and password; landed on onboarding | Privy sign-in not configured on this server, so not checked |
| `/login` | Yes | Real | Signed in already, it sends you to the console | The sign-in form itself was not filled in this pass |
| `/forgot-password`, `/reset-password` | Yes | | Forms not submitted | Reset without a token says "This reset link is missing its token". Sending the email needs a mail sender configured |
| `/onboarding` | Yes | Real | "Issue my first Spend Pass" completed onboarding and opened the pass form | The console redirects here until it is completed |
| `/terms`, `/privacy`, `/contact` | Yes | Static; contact shows only what is configured | | Privacy now covers demo requests |
| unknown path | Yes | | | A 404 page with links home and to the console |

## Console (signed in)

| Route | Renders | Real data | Actions | Notes |
|---|---|---|---|---|
| `/console` | Yes | Real: counts, 30-day spend, catalog totals | Links resolve | Empty state for a new account is clear |
| `/console/quickstart` | Yes | Real | **Demo A end to end**: sandbox pass created, API key issued (shown once), dry run (ALLOW, would pay sandbox:beta 0.001; nine sandbox and devnet sellers and ten mainnet ones refused, each with its code), executed, "Committed: paid once", "test payment, no money moved", answer and receipt shown | The refusal list is long for a first run |
| `/console/projects` | Yes | Real | **Trial started** (plan badge, "1 of 10 projects", button gone); project limit shown and enforced; usage after the run: 1 sandbox execution, 0.001 USDC simulated, 0 on-chain | No overflow at 375 px |
| `/console/passes` | Yes | Real | Empty state and form opened; on-chain panel not exercised | Wallet connect needs a browser wallet extension: **not tested** |
| `/console/executions` | Yes | Real | Empty state read before the run | |
| `/console/providers` | Yes | Real: 1,677 providers on mainnet across four catalogs | Filters not exercised | |
| `/console/providers/[id]` | Yes, **after a fix** | Real | Opened with a plain and a percent-encoded ID | Was broken: every provider answered "no such provider" because the ID was encoded twice. Fixed in this pass |
| `/console/intelligence` | Yes | Real: 6,131 endpoints, 1,725 providers, 3 quoted, 1 tested, 1 verified, 6,106 executable, 1 blocked; last refresh and each catalog's age | Refresh and trust buttons not clicked in this pass | "Suspicious changes" shows sandbox:shifty's payee change and block (Demo C). Everything is "new this week" because the registry is days old. "Executable" includes mainnet endpoints, and mainnet payments are off |
| `/console/routing` | Yes | Real: 14 classes, 717 endpoints classified, 266 routable | Probe not clicked | |
| `/console/firewall` | Yes | Real | Kill switch and dry run not clicked in this pass | Covered by HTTP tests |
| `/console/agent` | Yes | Real | No message sent | Needs a model API key on the server to answer; not checked here |
| `/console/connect` | Yes | Real | | Listed two MCP tools by their earlier names: fixed in this pass |
| `/console/settings` | Yes | Real: profile, sign-in methods, sessions, export, delete | Not exercised | |

## What this pass fixed

1. Provider detail pages (`/console/providers/[id]`) failed for every provider.
2. The Connect page named `algebra.discover_providers` and
   `algebra.execution_status`; the tools are now `algebra.discover` and
   `algebra.status`, with `algebra.quote` and `algebra.receipt` added.
3. The overview said only the sandbox could be paid, whatever the server had
   configured.
4. The pricing page listed member seats that cannot be used (nobody can be
   invited yet); removed, with a line saying so.

## Not checked, and why

- **Wallet connect** (Phantom, Solflare, Backpack) for the on-chain pass: needs
  a wallet extension in the browser.
- **Privy sign-in**: no Privy app is configured on this server.
- **The agent chat answering**: needs a model key; and sending a message can
  spend from a pass.
- **Password reset by email**: no mail sender configured locally.
- **Screenshots and visual review**: the browser pane was hidden, so layout was
  checked by measuring overflow at 375 px on the new and changed pages only
  (`/pricing`, `/book-demo`, `/console/projects`), not by eye.
- **Dark mode**, and the remaining console pages at mobile width.
- A production build was checked with `next build`, not by browsing it.

## Console errors and failed requests

None from the pages as they are now. The two errors seen during the pass were
a duplicate React key on the pricing page (fixed before commit) and the 400
from the deliberately invalid demo request.
