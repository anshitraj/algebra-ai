use anchor_lang::prelude::*;
use anchor_spl::token::{transfer_checked, Mint, Token, TokenAccount, TransferChecked};

use crate::{events::Refunded, state::Pass};

#[derive(Accounts)]
pub struct Refund<'info> {
    /// Whoever controls the pass's destination (for Algebra, the payer). Only
    /// they can move tokens out of it, so only they can return them.
    pub authority: Signer<'info>,
    #[account(
        mut,
        seeds = [Pass::SEED, pass.owner.as_ref(), pass.id.to_le_bytes().as_ref()],
        bump = pass.bump,
        has_one = mint,
        has_one = destination
    )]
    pub pass: Account<'info, Pass>,
    pub mint: Account<'info, Mint>,
    #[account(
        mut,
        associated_token::mint = mint,
        associated_token::authority = pass,
        associated_token::token_program = token_program
    )]
    pub vault: Account<'info, TokenAccount>,
    #[account(mut, token::mint = mint, token::authority = authority)]
    pub destination: Account<'info, TokenAccount>,
    pub token_program: Program<'info, Token>,
}

impl<'info> Refund<'info> {
    /// Puts `amount` back in the vault and gives it back to the budget: a pull
    /// whose payment then failed, or an escrow that settled for less. Works in
    /// any state, frozen, expired or revoked: returning money is always
    /// allowed. It can't return more than the pass has spent.
    pub fn refund(&mut self, amount: u64, intent: [u8; 32]) -> Result<()> {
        let now = Clock::get()?.unix_timestamp;
        self.pass.record_refund(now, amount)?;
        transfer_checked(
            CpiContext::new(
                self.token_program.key(),
                TransferChecked {
                    from: self.destination.to_account_info(),
                    mint: self.mint.to_account_info(),
                    to: self.vault.to_account_info(),
                    authority: self.authority.to_account_info(),
                },
            ),
            amount,
            self.mint.decimals,
        )?;
        emit!(Refunded {
            pass: self.pass.key(),
            amount,
            intent,
            spent: self.pass.spent,
            remaining_budget: self.pass.remaining_budget(),
        });
        Ok(())
    }
}
