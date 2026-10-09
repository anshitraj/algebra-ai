use anchor_lang::prelude::*;

#[error_code]
pub enum SpendPassError {
    #[msg("This pass has been revoked")]
    PassRevoked,
    #[msg("This pass is frozen by its owner")]
    PassFrozen,
    #[msg("This pass has expired")]
    PassExpired,
    #[msg("The amount must be more than zero")]
    ZeroAmount,
    #[msg("More than the pass's cap for one pull")]
    OverPerCallCap,
    #[msg("More than the pass's remaining budget")]
    OverBudget,
    #[msg("More than the pass's cap for the current window")]
    OverWindowCap,
    #[msg("The pass's terms are not valid")]
    InvalidTerms,
    #[msg("The agent can't be the owner or the zero address")]
    InvalidAgent,
    #[msg("The destination can't be the vault or an account the pass controls")]
    InvalidDestination,
    #[msg("The pass is already revoked")]
    AlreadyRevoked,
    #[msg("Arithmetic overflow")]
    Overflow,
    #[msg("More than the pass has spent")]
    RefundOverSpent,
}
