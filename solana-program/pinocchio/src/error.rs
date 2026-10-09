//! The same errors, with the same numbers, as the Anchor program
//! (programs/spend-pass/src/error.rs: Anchor numbers them from 6000), so
//! whatever reads a refusal reads it the same way from either program.

use pinocchio::error::ProgramError;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[repr(u32)]
pub enum SpendPassError {
    PassRevoked = 6000,
    PassFrozen,
    PassExpired,
    ZeroAmount,
    OverPerCallCap,
    OverBudget,
    OverWindowCap,
    InvalidTerms,
    InvalidAgent,
    InvalidDestination,
    AlreadyRevoked,
    Overflow,
    RefundOverSpent,
}

impl From<SpendPassError> for ProgramError {
    fn from(e: SpendPassError) -> Self {
        ProgramError::Custom(e as u32)
    }
}
