use anchor_lang::prelude::*;
use anchor_spl::token::{transfer_checked, Mint, Token, TokenAccount, TransferChecked};

use crate::{events::Pulled, state::Pass};

#[derive(Accounts)]
pub struct Pull<'info> {
    /// The key the owner named when making the pass.
    pub agent: Signer<'info>,
    #[account(
        mut,
        seeds = [Pass::SEED, pass.owner.as_ref(), pass.id.to_le_bytes().as_ref()],
        bump = pass.bump,
        has_one = agent,
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
    /// Must be the destination fixed when the pass was made.
    #[account(mut)]
    pub destination: Account<'info, TokenAccount>,
    pub token_program: Program<'info, Token>,
}

impl<'info> Pull<'info> {
    /// Takes `amount` from the vault to the pass's destination, if and only if
    /// every limit of the pass holds. `intent` is the hash of the request this
    /// pays for; it is kept on the pass and put in the event.
    pub fn pull(&mut self, amount: u64, intent: [u8; 32]) -> Result<()> {
        let now = Clock::get()?.unix_timestamp;
        self.pass.record_pull(now, amount, intent)?;

        let id = self.pass.id.to_le_bytes();
        let seeds: &[&[u8]] = &[Pass::SEED, self.pass.owner.as_ref(), id.as_ref(), &[self.pass.bump]];
        transfer_checked(
            CpiContext::new_with_signer(
                self.token_program.key(),
                TransferChecked {
                    from: self.vault.to_account_info(),
                    mint: self.mint.to_account_info(),
                    to: self.destination.to_account_info(),
                    authority: self.pass.to_account_info(),
                },
                &[seeds],
            ),
            amount,
            self.mint.decimals,
        )?;
        emit!(Pulled {
            pass: self.pass.key(),
            agent: self.agent.key(),
            amount,
            intent,
            spent: self.pass.spent,
            remaining_budget: self.pass.remaining_budget(),
        });
        Ok(())
    }
}
