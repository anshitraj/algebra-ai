use anchor_lang::prelude::*;
use anchor_spl::token::{
    close_account, transfer_checked, CloseAccount, Mint, Token, TokenAccount, TransferChecked,
};

use crate::{events::Closed, state::Pass};

#[derive(Accounts)]
pub struct ClosePass<'info> {
    #[account(mut)]
    pub owner: Signer<'info>,
    /// Closed at the end of the instruction: its rent goes back to the owner.
    #[account(
        mut,
        close = owner,
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

impl<'info> ClosePass<'info> {
    /// Returns everything left in the vault to the owner, then closes the vault
    /// and the pass, returning their rent too. Works in any state.
    pub fn close(&mut self) -> Result<()> {
        let id = self.pass.id.to_le_bytes();
        let seeds: &[&[u8]] = &[Pass::SEED, self.pass.owner.as_ref(), id.as_ref(), &[self.pass.bump]];
        let returned = self.vault.amount;
        if returned > 0 {
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
                returned,
                self.mint.decimals,
            )?;
        }
        close_account(CpiContext::new_with_signer(
            self.token_program.key(),
            CloseAccount {
                account: self.vault.to_account_info(),
                destination: self.owner.to_account_info(),
                authority: self.pass.to_account_info(),
            },
            &[seeds],
        ))?;
        emit!(Closed { pass: self.pass.key(), returned });
        Ok(())
    }
}
