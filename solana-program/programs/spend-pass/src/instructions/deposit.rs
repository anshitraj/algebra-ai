use anchor_lang::prelude::*;
use anchor_spl::token::{transfer_checked, Mint, Token, TokenAccount, TransferChecked};

use crate::{error::SpendPassError, events::Deposited, state::Pass};

#[derive(Accounts)]
pub struct Deposit<'info> {
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

impl<'info> Deposit<'info> {
    /// Moves `amount` from the owner's token account into the vault. Anyone can
    /// also send tokens straight to the vault's address; this only adds the
    /// checks and the event.
    pub fn deposit(&mut self, amount: u64) -> Result<()> {
        require!(!self.pass.revoked, SpendPassError::PassRevoked);
        require!(amount > 0, SpendPassError::ZeroAmount);
        transfer_checked(
            CpiContext::new(
                self.token_program.key(),
                TransferChecked {
                    from: self.owner_token.to_account_info(),
                    mint: self.mint.to_account_info(),
                    to: self.vault.to_account_info(),
                    authority: self.owner.to_account_info(),
                },
            ),
            amount,
            self.mint.decimals,
        )?;
        emit!(Deposited { pass: self.pass.key(), amount });
        Ok(())
    }
}
