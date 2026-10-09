use anchor_lang::prelude::*;
use anchor_spl::{
    associated_token::AssociatedToken,
    token::{Mint, Token, TokenAccount},
};

use crate::{
    error::SpendPassError,
    events::PassCreated,
    state::{Pass, PassTerms},
};

#[derive(Accounts)]
#[instruction(id: u64)]
pub struct CreatePass<'info> {
    /// The person the money belongs to. Pays the rent.
    #[account(mut)]
    pub owner: Signer<'info>,
    /// CHECK: only its address is read. It is stored as the one key that may
    /// pull from this pass.
    pub agent: UncheckedAccount<'info>,
    pub mint: Account<'info, Mint>,
    /// The token account every pull is paid to (for Algebra, the payer's USDC
    /// account). Only its mint is checked here.
    #[account(token::mint = mint)]
    pub destination: Account<'info, TokenAccount>,
    #[account(
        init,
        payer = owner,
        seeds = [Pass::SEED, owner.key().as_ref(), id.to_le_bytes().as_ref()],
        bump,
        space = Pass::DISCRIMINATOR.len() + Pass::INIT_SPACE
    )]
    pub pass: Account<'info, Pass>,
    /// Holds the pass's tokens. Its authority is the pass itself, so nothing
    /// moves out of it except through this program.
    #[account(
        init,
        payer = owner,
        associated_token::mint = mint,
        associated_token::authority = pass,
        associated_token::token_program = token_program
    )]
    pub vault: Account<'info, TokenAccount>,
    pub associated_token_program: Program<'info, AssociatedToken>,
    pub token_program: Program<'info, Token>,
    pub system_program: Program<'info, System>,
}

impl<'info> CreatePass<'info> {
    pub fn create(&mut self, id: u64, terms: PassTerms, bumps: &CreatePassBumps) -> Result<()> {
        let now = Clock::get()?.unix_timestamp;
        terms.validate(now)?;
        require!(
            self.agent.key() != self.owner.key() && self.agent.key() != Pubkey::default(),
            SpendPassError::InvalidAgent
        );
        require!(
            self.destination.key() != self.vault.key() && self.destination.owner != self.pass.key(),
            SpendPassError::InvalidDestination
        );

        self.pass.set_inner(Pass {
            owner: self.owner.key(),
            agent: self.agent.key(),
            mint: self.mint.key(),
            destination: self.destination.key(),
            id,
            per_call_cap: terms.per_call_cap,
            total_budget: terms.total_budget,
            spent: 0,
            window_secs: terms.window_secs,
            window_cap: terms.window_cap,
            window_start: now,
            window_spent: 0,
            expires_at: terms.expires_at,
            created_at: now,
            pulls: 0,
            last_intent: [0; 32],
            frozen: false,
            revoked: false,
            bump: bumps.pass,
        });
        emit!(PassCreated {
            pass: self.pass.key(),
            owner: self.owner.key(),
            agent: self.agent.key(),
            mint: self.mint.key(),
            destination: self.destination.key(),
            per_call_cap: terms.per_call_cap,
            total_budget: terms.total_budget,
            window_secs: terms.window_secs,
            window_cap: terms.window_cap,
            expires_at: terms.expires_at,
        });
        Ok(())
    }
}
