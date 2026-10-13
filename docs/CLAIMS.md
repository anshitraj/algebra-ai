# What is proven, and what is not

Every claim Algebra makes, with the test or the transaction behind it. A claim
with no evidence listed here should not be made in a pitch, a README or a UI.
Test paths are under `backend/` unless they start with `solana-program/`.

Evidence comes in four kinds, and they are not interchangeable:

| Kind | Means |
|---|---|
| **Chain** | A finalized transaction on Solana devnet, linked from the docs |
| **HTTP test** | An end-to-end test over real HTTP (and MCP) against real Postgres, with the sandbox rail and stand-ins for anything outside the machine |
| **Unit test** | A test of the rule itself, no I/O |
| **Browser** | Done by hand in a browser against a local server, on the date given |

## Proven

### Developer platform

| Claim | Evidence |
|---|---|
| A new account gets an organization and a first project, and can issue a key at once | HTTP test `TestTenancyOverHTTP_AKeyRunsInItsProjectAndEnvironmentOnly`; Browser 2026-10-12 |
| An API key's secret is shown once; only its hash is stored; listing keys never shows it | HTTP test, same; unit test `TestGeneratedKeysAreDistinctPrefixedAndOnlyTheHashIsKept` |
| A key works only in its own project and environment: a sandbox key cannot reach devnet or mainnet | HTTP test, same; unit tests `TestEnvironmentsMapToOneNetworkEach`, `TestEconomic_AProjectPassPaysOnlyInItsEnvironment` |
| A key never spends on its own: without a Spend Pass of its project and environment it is refused; scopes are enforced | HTTP test `TestTenancyOverHTTP_ScopesAndMissingPasses` |
| A key cannot create, rotate or revoke keys, start a trial or read usage: those need a person's session | HTTP tests `TestTenancyOverHTTP_*`, `TestTrialOverHTTP_*`, `TestUsageOverHTTP_*` |
| Nothing of one person's is visible to another: organizations, projects, keys, passes, events, usage | HTTP tests `TestTenancyOverHTTP_NothingCrossesBetweenPeople`, `TestUsageOverHTTP_*`, `TestTrialOverHTTP_*` |
| Free includes one project, enforced; the trial lifts it at once | HTTP test `TestTrialOverHTTP_SevenDaysOfStartupOncePerOrganization` |
| The Startup trial lasts seven days, is one per organization, asks for no card, and ends on its own | HTTP test, same; unit test `TestATrialGivesItsPlanOnlyWhileItRuns` |
| One account owns at most three organizations and gets one trial, whichever organization takes it | HTTP test `TestLimitsOverHTTP_OrganizationsPerPersonAndOneTrialEach` |
| Each new Book a Demo enquiry is emailed to the operator when `LEADS_NOTIFY_EMAIL` is set, after it is stored | Unit tests `TestLeads_TheOperatorIsEmailedAfterTheEnquiryIsStored`, `TestLeads_NoNotificationWithoutAnAddress`. **No email has been sent through Resend from this build** |
| A project's use is metered: requests by state, committed executions and spend by network; sandbox runs are counted but not as managed executions; a repeat is counted once | HTTP test `TestUsageOverHTTP_AProjectsSandboxRunsAreCountedButNotBilled` |
| A Book a Demo enquiry is validated, stored, and readable only with the operator token | HTTP test `TestLeadsOverHTTP_AnEnquiryIsStoredAndOnlyTheOperatorReadsIt`; Browser 2026-10-12 |
| The pricing API says billing is not live | HTTP test `TestPlansOverHTTP_ThePricingPageNeverClaimsLiveBilling` |

### MCP

| Claim | Evidence |
|---|---|
| The default tools are `algebra.discover`, `quote`, `simulate`, `execute`, `status`, `receipt`, `spend_pass` (with `classes` and `discover_web`); the shopping tools and earlier names need `MCP_LEGACY_TOOLS=on` | HTTP tests `TestMCPOverHTTP_AnAgentExecutesThroughTheSandboxWithItsPassToken`, `TestMCPOverHTTP_TheLegacyToolsNeedTheirFlag` |
| Over MCP an agent quotes, simulates, executes, repeats without paying twice, reads the status and a receipt that verifies | HTTP test, the first above |
| No token, no payment; another person's receipt cannot be read | HTTP tests `TestMCPOverHTTP_NoTokenNoPayment`, the first above |

### Routing and guards

| Claim | Evidence |
|---|---|
| A class of work is priced across its providers and paid to the best honest one | HTTP test `TestRouterOverHTTP_AClassIsPaidToTheBestHonestProvider`; Chain (DEMO.md, 2026-10-07 and 2026-10-12) |
| A provider that is down, asks more than it lists, or is priced like a trap is refused before payment | Unit tests `TestGuardRefusesALivePriceAboveTheListing`, `TestGuardRefusesHoneypotPrices`, `TestGuardSkipsDownProvidersAndOtherNetworks`; Chain (DEMO.md) |
| A dry run answers without creating, reserving or paying | HTTP test `TestSimulateOverHTTP_AnswersWithoutPaying` |
| Cheapest, fastest and auto rank differently and the strategy is respected | `internal/domain/routing` unit tests, `internal/app/execution_router_test.go` |
| Fastest ranks on measured speed: Algebra's own paid calls when it has them, else the time of the unpaid price check; with no speed known it ranks by price and says so | Unit tests `TestRankFastestPrefersPaidCallsOverAPriceCheck`, `TestRankFastestTimesANewProviderByItsPriceCheck`, `TestRankFastestSaysWhenNoSpeedIsKnown` |
| A dry run ranks on the same record as the execution, stays in a project pass's environment, and never shows an unlisted price as free | Unit tests `TestSimulate_RanksOnTheSameRecordAsTheExecution`, `TestSimulate_AProjectPassStaysInItsEnvironment`, `TestSimulate_AnUnlistedPriceIsNotFree` |
| The chat never pays on the model's say: it shows the compared offers as cards (the best each way: cheapest, balanced, fastest), several pieces of work at once, and pays only the cards the person picks. Their press is recorded as their approval | Browser 2026-10-13, sandbox (two pieces of work compared, both paid with one press, "approved by the account owner" on each receipt); Chain 2026-10-13: the chat route on devnet, one card chosen, tx `5z5S2V9kV8Mp9hU6Gf57DUH3diwXv1PAj2hsVDQZxs1gW5ivYpfiCAe86Xx6KUSxZnDSzBfs93zebh5UMo27r1ym` (finalized; the seller is Algebra's own demo provider). The same card sent twice paid once |
| A paid call has a PDF receipt, made from the signed receipt and readable only by its owner, with the transaction linked on Solana Explorer and Solscan | HTTP test `TestReceiptPDFOverHTTP`; unit test `TestDocumentIsAWellFormedPDF`; Browser 2026-10-13 (sandbox); the devnet payment above (PDF read by hand) |
| The production build sends its Content-Security-Policy and the policy lets the browser reach the Solana RPC endpoints, Jupiter and Privy; every page renders from the build; the console is closed without a session | `frontend/scripts/smoke.mjs` (`pnpm build && pnpm smoke`, in CI): 52 checks against the standalone server with a stub API. **No browser runs in it: a page's own JavaScript is not exercised** |
| An API key authenticates REST and MCP: `/execute`, `/pass`, `/providers` and the MCP handshake, as the Connect page shows them | HTTP tests `TestTenancyOverHTTP_*`; by hand 2026-10-13 with a sandbox key (delivered, signed receipt; key revoked after) |

### Policy

| Claim | Evidence |
|---|---|
| A frozen pass pays nothing, even for a payment already in flight | HTTP test `TestKillSwitchOverHTTP_AFrozenPassPaysNothing`; Chain (DEMO.md, kill switch row) |
| A first payment to a new provider waits for the person when the pass says so | HTTP test `TestNewProviderGateOverHTTP_TheFirstPaymentWaitsForThePerson`; Chain (DEMO.md) |
| A looping agent is stopped by the per-minute cap | HTTP test `TestVelocityOverHTTP_ALoopingAgentIsStopped` |
| An agent can never approve its own spending | `internal/app/authz_test.go`, `internal/app/policy_controls_test.go` |
| No mainnet payment is signed unless `MAINNET_EXECUTION=on` | Unit test `TestEconomic_NoMainnetPaymentIsSignedUnlessEnabled` |
| `MAINNET_EXECUTION=on` is refused at startup unless `SPEND_PASS_REQUIRED` includes mainnet | Unit test `TestMainnetExecutionNeedsOnChainPassesRequired` |
| The server reads the program's upgrade authority from the cluster; with mainnet execution on it refuses to start if that authority is its own payer key or cannot be read | Unit tests `TestProgramAccountsNameTheirUpgradeAuthority`, `TestMainnetReadiness_EveryBlockingGateIsNeededToPay`; seen live on devnet 2026-10-12 (startup warning naming the key) |
| `GET /api/v1/mainnet/readiness` reports every gate, whether it passes and why not; no configuration makes an unreviewed program "ready for customer funds" | Unit tests `TestMainnetReadiness_*`; HTTP test `TestMainnetReadinessOverHTTP_AServerWithNoMainnetSetUpSaysWhyItCannotPay` |
| Demo accounts pay in the sandbox only | HTTP test `TestDemoAccountOverHTTP_PaysInTheSandboxOnly` |

### Provider trust

| Claim | Evidence |
|---|---|
| A new listing is unverified, with its payee pinned | Unit test `TestMerge_NewEndpointIsUnverifiedWithItsPayeePinned` |
| A pass can hold or refuse a provider whose identity is unverified: "ask me first" waits for a yes and falls back to a verified provider, "never" pays none | Unit tests `TestUnverifiedRule`, `TestUnverifiedRule_ApproveHoldsAndFallsBackToAVerifiedProvider`, `TestUnverifiedRule_DenyNeverPaysAnUnverifiedProvider` |
| Every provider and endpoint shows what the registry knows about it (verified, unverified, blocked, payment tested, payee pinned): on the Providers pages, in the chat's search results and on each compared offer | Browser 2026-10-13 (Providers list: 99 of the first 100 rows unverified, 69 with a pinned payee, none verified; chat cards in the sandbox) |
| A provider that changes its payee is refused at payment time with `PAYEE_CHANGED`, then blocked | HTTP test `TestProviderTrustOverHTTP_APayeeChangeIsRefusedThenBlocked`; unit test `TestMerge_ListingPayeeChangeBlocks`; Browser 2026-10-12 (reason shown in Quickstart and Provider intelligence) |
| A paid delivery makes an endpoint payment tested and never verifies it, however many times it delivers | Unit test `TestEndpoint_ADeliveryTestsItAndNeverVerifiesIt`; HTTP test `TestProviderTrustOverHTTP_APaidDeliveryTestsASellerAndOnlyAReviewVerifiesIt` |
| Verifying needs all six checks of the standard (listed, HTTPS named host, quoted, payee pinned and consistent, payment tested, not blocked) and a person's written evidence; each check failing on its own refuses it | Unit test `TestSetTrust_VerifyingNeedsEveryCheckAndTheEvidenceReviewed`; the HTTP test above; Postgres test `TestIntelRefresh_*` |
| A payee change voids verification and testing: after an unblock both must be earned again | Unit test `TestEndpoint_APayeeChangeVoidsVerificationAndTesting` |
| Trust decisions are a signed-in person's, never a key's | HTTP test `TestProviderRegistryOverHTTP_OpenToReadDecidedByAPerson` |
| Price changes, removals and restores are recorded; an unchanged listing records nothing | Unit tests `TestMerge_PriceRemovalAndRestore`, `TestMerge_UnchangedListingRecordsNoChange` |
| The refresh is due at 00:00, 06:00, 12:00 and 18:00 UTC | Unit test `TestNextRun_SixHourSlotsUTC` |
| A refresh detects a new endpoint, a re-priced one (suspicious when it more than doubles) and a re-payee'd one (blocked); a blocked endpoint is refused at its new payee and at its old one; a listing never verifies itself | Postgres test `TestIntelRefresh_NewChangedAndRemovedEndpointsAndTheirTrust` (`internal/platform/postgres`), on a stand-in catalog |
| A catalog that fails, or answers with nothing, keeps its last good copy; a dropped endpoint is marked removed and keeps its history and its block | the same test |
| With several instances exactly one runs the refresh; a held lease is not taken, even on demand; only its owner releases it; an expired lease can be taken over | Postgres test `TestScheduledJob_ExactlyOneInstanceTakesTheLease` (sixteen concurrent claimers) |
| The refresh ran against the four live catalogs and filled the registry (6,131 endpoints, 1,725 providers on 2026-10-12) | Browser 2026-10-12, Provider intelligence page |

### Never twice, and receipts

| Claim | Evidence |
|---|---|
| The same request again returns the kept answer and pays nothing | HTTP test `TestExecuteOverHTTP_ARepeatRequestIsAnsweredFromTheKeptResult`; Chain: one pull and one payment for two identical requests (DEMO.md, 2026-10-12) |
| At most one live attempt and one commitment per intent, enforced by the database | `internal/platform/postgres` integration tests; `internal/app/economic_service_test.go`, `economic_gaps_test.go` |
| An ambiguous outcome freezes and is settled from the rail's evidence | `internal/app/economic_service_test.go`; `providers/solanax402` tests |
| Every committed outcome has an Ed25519 receipt anyone can verify | HTTP tests above; the devnet receipt of 2026-10-12 verified at `POST /api/v1/receipts/verify` |

### Email notifications

| Claim | Evidence |
|---|---|
| A sign-in is emailed to the address the person signs in with: when, which device and IP, and how to sign that device out. A first sign-in gets a welcome instead. At most one a minute per account | Unit test `TestNotifications_ASignInIsEmailedToTheAddressTheySignInWith`; HTTP test `TestNotificationsOverHTTP_SignInAlertsAndAConfirmedAddress` |
| A payment that lands on a chain is emailed to its person with the transaction, both explorer links and the PDF receipt attached. A sandbox call, a free call and a demo account send nothing. At most 20 an hour per account | Unit tests `TestNotifications_AReceiptIsEmailedForAPaymentOnAChain`, `TestNotifications_ReceiptEmailsStopAtTheHourlyCap`; by hand 2026-10-13: a devnet payment (tx `5KZ98M2ZZx4XLD3JvpJTHhGtETPqFRWizT613eNvfcJNApyMRNrU499djRnFijsmi3HsETwgJjtwNAvsFFToBeCZ`) produced the email with a 9 KB PDF, on a server whose mailer writes to its log |
| An account that signed in with a wallet has nowhere to be written to and is told so on every screen. An address it adds gets only a confirmation link until the same account opens it; a link works once, lasts a day, and one is sent a minute at most | Unit tests `TestNotifications_AWalletAccountGetsNothingUntilItConfirmsAnAddress`, `TestNotifications_ConfirmationLinksAreRateLimitedExpireAndReplaceEachOther`; HTTP test `TestNotificationsOverHTTP_AWalletAccountIsAskedForAnEmail`; Browser 2026-10-13 (banner, add, confirm from the link, banner gone) |
| An email a wallet account's person later verifies with Privy is used without a second confirmation | HTTP test `TestNotificationsOverHTTP_AWalletAccountIsAskedForAnEmail` |
| Where someone's mail goes is theirs alone: no session, no answer; an API key reads and changes nothing. Each kind of email can be turned off | HTTP test `TestNotificationsOverHTTP_SignInAlertsAndAConfirmedAddress`; unit test `TestNotifications_EachKindCanBeTurnedOff` |
| What Resend is sent: the sender, one recipient, the HTML with the plain text beside it, and the PDF as base64 | Unit tests `TestResendMailerSendsTextAndAnAttachment`, `TestResendMailerSendsHTMLBesideTheText`, **against a stand-in for Resend** |
| Every email is one layout (the mark, a card, facts, one button), laid out with tables and inline styles in a single fluid column up to 560px, so it is the same email on a phone and on a desktop; the HTML and the plain text say the same thing; nothing someone else wrote reaches the HTML as markup | Unit tests `TestEmailDoc_HTMLAndTextSayTheSameThing`, `TestEmailDoc_LaysOutForMailClients`, `TestEmailDoc_OtherPeoplesTextNeverBecomesMarkup`, `TestSendEmail_UsesTheBestTheMailerCanCarry`; Browser 2026-10-13 (receipt and sign-in emails at 390px and 1280px wide). **Not looked at in Gmail, Outlook or Apple Mail themselves** |

### Solana

| Claim | Evidence |
|---|---|
| The Spend Pass program is deployed on devnet at `46gQDUuJCt6VdDMaguGPoD8SKEtFqvpG7ECtpC2F9tdS` | Chain: [program](https://explorer.solana.com/address/46gQDUuJCt6VdDMaguGPoD8SKEtFqvpG7ECtpC2F9tdS?cluster=devnet), deploy transaction in SPEND_PASS_PROGRAM.md |
| The program enforces per-call cap, budget, window, expiry, freeze, revoke, signer and destination | `solana-program/programs/spend-pass/src/state.rs` unit tests; `solana-program/programs/spend-pass/tests/mod.rs` LiteSVM tests against the built program |
| A paid request through the API was funded by an on-chain pull, refused by an on-chain freeze, and refunded from chain proof | Chain: SPEND_PASS_PROGRAM.md (2026-10-09) |
| A project's devnet API key paid from an on-chain pass vault | Chain: DEMO.md (2026-10-12), three finalized transactions |
| After the trust and mainnet-gate changes a project's devnet API key still pays on devnet, a repeat pays nothing, the receipt verifies, and the same key is refused on mainnet (`ENVIRONMENT_NETWORK_MISMATCH`) | Chain: DEMO.md (2026-10-12, second run), one finalized transaction |
| x402 `exact` and `upto` (payment channel) payments settle on devnet | Chain: DEMO.md (2026-10-07) |
| A pull's state moves only by compare-and-set, so two processes cannot both refund it | Unit test `TestPullStateMovesOnlyFromWhereItWas` |

### Swaps

| Claim | Evidence |
|---|---|
| A swap is quoted, simulated, and signed only if it does exactly what it says | HTTP tests `TestSwapOverHTTP_*`, **on a fake cluster with a fake Jupiter** |

## Not proven: do not claim

| Do not say | Because |
|---|---|
| "Works on mainnet" / "live on mainnet" | No mainnet payment has been made. The program is not deployed on mainnet. See [MAINNET_READINESS.md](MAINNET_READINESS.md) |
| "Jupiter swaps executed" | No swap has landed on any chain. Tested against a fake cluster only |
| "Audited" | Neither the program nor the server has had an independent review |
| "Verified provider" as a statement about the seller's legal identity, reputation or future safety | The check mark means the six checks passed and a person recorded the evidence they reviewed. On 2026-10-12 no catalog endpoint is verified |
| "Verified" for an endpoint that has only been paid | That is *payment tested*. It proves one transaction worked |
| "Paid a third-party provider on devnet" | The devnet sellers are Algebra's own demo provider. Third-party providers were priced and their payments simulated on mainnet, never paid |
| "Subscriptions" / "billing" for the five plans | No payment processor is connected to them. (Razorpay code exists for the original product's two plans; it is not wired to these) |
| "Works with ChatGPT / Claude / Cursor" as a certified integration | The MCP server is tested with the official Go SDK client. We have no recorded run of each named client against this build |
| "Monthly execution limits enforced" | Usage is counted and shown. Only the project limit is enforced; nothing is refused at the execution allowance |
| "Team seats" | Roles exist in the schema; there is no way to invite a second person |
| "Wallet connect tested" for Demo B | The owner's transaction was signed from a keypair file, not a browser wallet extension |
| "Every listed API is executable" | Listed is not executable. Base-only and fiat-billed providers are listed and cannot be paid |
| "Sign-in and receipt emails are delivered" | No email has been sent through Resend from this build: the emails were read from a server's log and from a stand-in for Resend. Delivery needs `RESEND_API_KEY` and an `EMAIL_FROM` on a domain verified in Resend |

## Planned, not built

- A payment processor for the five plans, and refusing requests past a plan's allowance
- Inviting members into an organization
- A TypeScript SDK (REST and MCP are the interfaces)
- Customer webhooks for execution events on the new platform
- A provider credential vault, and calling fiat-billed APIs with the customer's own key
- OAuth 2.1 for hosted MCP connectors
- Parallel as an optional discovery source
- Asynchronous jobs and batch orchestration
- Base, CCTP, cards, cross-chain balances, enterprise SSO
