use anchor_lang::prelude::*;

use crate::{
    error::SpendPassError,
    events::{AgentChanged, FrozenChanged, Revoked},
    state::Pass,
};

/// What only the owner may do to a pass that doesn't move tokens.
#[derive(Accounts)]
pub struct Manage<'info> {
    pub owner: Signer<'info>,
    #[account(
        mut,
        seeds = [Pass::SEED, pass.owner.as_ref(), pass.id.to_le_bytes().as_ref()],
        bump = pass.bump,
        has_one = owner
    )]
    pub pass: Account<'info, Pass>,
}

impl<'info> Manage<'info> {
    /// The kill switch for one pass: while frozen, nothing can be pulled. The
    /// owner can lift it.
    pub fn set_frozen(&mut self, frozen: bool) -> Result<()> {
        self.pass.frozen = frozen;
        emit!(FrozenChanged { pass: self.pass.key(), frozen });
        Ok(())
    }

    /// Replaces the key that may pull, for instance after it leaks.
    pub fn set_agent(&mut self, agent: Pubkey) -> Result<()> {
        require!(
            agent != self.owner.key() && agent != Pubkey::default(),
            SpendPassError::InvalidAgent
        );
        self.pass.agent = agent;
        emit!(AgentChanged { pass: self.pass.key(), agent });
        Ok(())
    }

    /// Ends the pass for good: nothing can be pulled again, and it can't be
    /// undone. The tokens stay in the vault until the owner withdraws or
    /// closes the pass.
    pub fn revoke(&mut self) -> Result<()> {
        require!(!self.pass.revoked, SpendPassError::AlreadyRevoked);
        self.pass.revoked = true;
        emit!(Revoked { pass: self.pass.key() });
        Ok(())
    }
}
