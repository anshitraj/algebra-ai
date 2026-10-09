use anchor_lang::prelude::*;

// Every state change leaves an event in the transaction logs, so a pass's whole
// history can be rebuilt from the chain without trusting any server.

#[event]
pub struct PassCreated {
    pub pass: Pubkey,
    pub owner: Pubkey,
    pub agent: Pubkey,
    pub mint: Pubkey,
    pub destination: Pubkey,
    pub per_call_cap: u64,
    pub total_budget: u64,
    pub window_secs: i64,
    pub window_cap: u64,
    pub expires_at: i64,
}

#[event]
pub struct Deposited {
    pub pass: Pubkey,
    pub amount: u64,
}

/// `intent` is the hash of the request this pull paid for; it ties the pull to
/// Algebra's signed receipt for that request.
#[event]
pub struct Pulled {
    pub pass: Pubkey,
    pub agent: Pubkey,
    pub amount: u64,
    pub intent: [u8; 32],
    pub spent: u64,
    pub remaining_budget: u64,
}

/// Tokens a pull didn't use, put back by the destination's authority.
#[event]
pub struct Refunded {
    pub pass: Pubkey,
    pub amount: u64,
    pub intent: [u8; 32],
    pub spent: u64,
    pub remaining_budget: u64,
}

#[event]
pub struct FrozenChanged {
    pub pass: Pubkey,
    pub frozen: bool,
}

#[event]
pub struct AgentChanged {
    pub pass: Pubkey,
    pub agent: Pubkey,
}

#[event]
pub struct Withdrawn {
    pub pass: Pubkey,
    pub amount: u64,
}

#[event]
pub struct Revoked {
    pub pass: Pubkey,
}

#[event]
pub struct Closed {
    pub pass: Pubkey,
    pub returned: u64,
}
