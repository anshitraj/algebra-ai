//! Algebra Spend Pass: permission to spend, not access to money, enforced on
//! Solana.
//!
//! A person (the owner) makes a pass for an agent key. The pass owns a vault of
//! the person's USDC. The agent can take money out of it only through `pull`,
//! only to one token account the owner fixed when making the pass, and only
//! within limits the program checks on every pull: a cap per pull, a total
//! budget, a cap per time window, and an expiry. The owner can freeze the pass,
//! replace the agent, withdraw, revoke or close at any time, with no one else's
//! signature. Algebra's router uses `pull` to fund its payer just before it pays
//! an x402 provider, so a person's money never sits in a shared wallet.
//!
//! Instructions: create_pass, deposit, pull, refund, set_frozen, set_agent,
//! withdraw, revoke, close_pass. Accounts and events are in `state` and `events`.

use anchor_lang::prelude::*;

pub mod error;
pub mod events;
pub mod instructions;
pub mod state;

pub use instructions::*;
pub use state::{Pass, PassTerms};

declare_id!("46gQDUuJCt6VdDMaguGPoD8SKEtFqvpG7ECtpC2F9tdS");

#[program]
pub mod spend_pass {
    use super::*;

    /// Makes a pass and its vault. `id` is the owner's own number for it.
    #[instruction(discriminator = 0)]
    pub fn create_pass(ctx: Context<CreatePass>, id: u64, terms: PassTerms) -> Result<()> {
        ctx.accounts.create(id, terms, &ctx.bumps)
    }

    /// Owner: puts tokens in the vault.
    #[instruction(discriminator = 1)]
    pub fn deposit(ctx: Context<Deposit>, amount: u64) -> Result<()> {
        ctx.accounts.deposit(amount)
    }

    /// Agent: takes tokens to the pass's destination, within its limits.
    #[instruction(discriminator = 2)]
    pub fn pull(ctx: Context<Pull>, amount: u64, intent: [u8; 32]) -> Result<()> {
        ctx.accounts.pull(amount, intent)
    }

    /// Owner: freezes or unfreezes the pass.
    #[instruction(discriminator = 3)]
    pub fn set_frozen(ctx: Context<Manage>, frozen: bool) -> Result<()> {
        ctx.accounts.set_frozen(frozen)
    }

    /// Owner: replaces the key that may pull.
    #[instruction(discriminator = 4)]
    pub fn set_agent(ctx: Context<Manage>, agent: Pubkey) -> Result<()> {
        ctx.accounts.set_agent(agent)
    }

    /// Owner: takes tokens back out of the vault.
    #[instruction(discriminator = 5)]
    pub fn withdraw(ctx: Context<Withdraw>, amount: u64) -> Result<()> {
        ctx.accounts.withdraw(amount)
    }

    /// Owner: ends the pass for good.
    #[instruction(discriminator = 6)]
    pub fn revoke(ctx: Context<Manage>) -> Result<()> {
        ctx.accounts.revoke()
    }

    /// Owner: returns everything in the vault and closes the vault and the pass.
    #[instruction(discriminator = 7)]
    pub fn close_pass(ctx: Context<ClosePass>) -> Result<()> {
        ctx.accounts.close()
    }

    /// Destination's authority: puts back what a pull didn't use and gives it
    /// back to the budget.
    #[instruction(discriminator = 8)]
    pub fn refund(ctx: Context<Refund>, amount: u64, intent: [u8; 32]) -> Result<()> {
        ctx.accounts.refund(amount, intent)
    }
}
