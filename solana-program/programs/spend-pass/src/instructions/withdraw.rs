use anchor_lang::prelude::*;
use anchor_spl::token::{transfer_checked, Mint, Token, TokenAccount, TransferChecked};

use crate::{error::SpendPassError, events::Withdrawn, state::Pass};

#[derive(Accounts)]
pub struct Withdraw<'info> {
    pub owner: Signer<'info>,
    #[account(
        seeds = [Pass::SEED, pass.owner.as_ref(), pass.id.to_le_bytes().as_ref()],
        bump = pass.bump,
        has_one = owner,
        has_one = mint
    )]
    pub pass: Account<'info, Pass>,
    pub mint: Account<'info, Mint>,
    #[account(mut, token::mint = mint, token::authority = owner)]
    pub owner_token: Account<'info, TokenAccount>,
    #[account(
        mut,
        associated_token::mint = mint,
        associated_token::authority = pass,
        associated_token::token_program = token_program
    )]
    pub vault: Account<'info, TokenAccount>,
    pub token_program: Program<'info, Token>,
}

impl<'info> Withdraw<'info> {
    /// Takes `amount` back out of the vault. The owner can do this whatever the
    /// pass's state: frozen, expired or revoked, the money is theirs.
    pub fn withdraw(&mut self, amount: u64) -> Result<()> {
        require!(amount > 0, SpendPassError::ZeroAmount);
        let id = self.pass.id.to_le_bytes();
        let seeds: &[&[u8]] = &[Pass::SEED, self.pass.owner.as_ref(), id.as_ref(), &[self.pass.bump]];
        transfer_checked(
            CpiContext::new_with_signer(
                self.token_program.key(),
                TransferChecked {
                    from: self.vault.to_account_info(),
                    mint: self.mint.to_account_info(),
                    to: self.owner_token.to_account_info(),
                    authority: self.pass.to_account_info(),
                },
                &[seeds],
            ),
            amount,
            self.mint.decimals,
        )?;
        emit!(Withdrawn { pass: self.pass.key(), amount });
        Ok(())
    }
}
