//! The pass account, byte for byte the same as the Anchor program's
//! `state::Pass` (a one-byte discriminator, then the fields in order, Borsh
//! little-endian), and the same limit arithmetic.

use pinocchio::{error::ProgramError, Address};

use crate::error::SpendPassError;

pub const DISCRIMINATOR: u8 = 1;
pub const SEED: &[u8] = b"pass";
/// 1 + 4 keys + 11 eight-byte fields + the intent + frozen, revoked, bump.
pub const LEN: usize = 1 + 4 * 32 + 11 * 8 + 32 + 3;

/// What an owner fixes when making a pass.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Terms {
    pub per_call_cap: u64,
    pub total_budget: u64,
    pub window_secs: i64,
    pub window_cap: u64,
    pub expires_at: i64,
}

impl Terms {
    pub const LEN: usize = 40;

    pub fn read(b: &[u8]) -> Result<Self, ProgramError> {
        if b.len() < Self::LEN {
            return Err(ProgramError::InvalidInstructionData);
        }
        Ok(Self {
            per_call_cap: u64_at(b, 0),
            total_budget: u64_at(b, 8),
            window_secs: u64_at(b, 16) as i64,
            window_cap: u64_at(b, 24),
            expires_at: u64_at(b, 32) as i64,
        })
    }

    pub fn validate(&self, now: i64) -> Result<(), ProgramError> {
        let ok = self.per_call_cap > 0
            && self.total_budget >= self.per_call_cap
            && self.expires_at > now
            && if self.window_secs == 0 {
                self.window_cap == 0
            } else {
                self.window_secs > 0 && self.window_cap >= self.per_call_cap
            };
        if ok {
            Ok(())
        } else {
            Err(SpendPassError::InvalidTerms.into())
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Pass {
    pub owner: Address,
    pub agent: Address,
    pub mint: Address,
    pub destination: Address,
    pub id: u64,
    pub per_call_cap: u64,
    pub total_budget: u64,
    pub spent: u64,
    pub window_secs: i64,
    pub window_cap: u64,
    pub window_start: i64,
    pub window_spent: u64,
    pub expires_at: i64,
    pub created_at: i64,
    pub pulls: u64,
    pub last_intent: [u8; 32],
    pub frozen: bool,
    pub revoked: bool,
    pub bump: u8,
}

#[inline(always)]
pub fn u64_at(b: &[u8], at: usize) -> u64 {
    let mut a = [0u8; 8];
    a.copy_from_slice(&b[at..at + 8]);
    u64::from_le_bytes(a)
}

#[inline(always)]
pub fn key_at(b: &[u8], at: usize) -> Address {
    let mut a = [0u8; 32];
    a.copy_from_slice(&b[at..at + 32]);
    Address::new_from_array(a)
}

impl Pass {
    /// Reads a pass account's data. Anything that isn't one is refused.
    pub fn read(d: &[u8]) -> Result<Self, ProgramError> {
        if d.len() < LEN || d[0] != DISCRIMINATOR || d[249] > 1 || d[250] > 1 {
            return Err(ProgramError::InvalidAccountData);
        }
        let mut last_intent = [0u8; 32];
        last_intent.copy_from_slice(&d[217..249]);
        Ok(Self {
            owner: key_at(d, 1),
            agent: key_at(d, 33),
            mint: key_at(d, 65),
            destination: key_at(d, 97),
            id: u64_at(d, 129),
            per_call_cap: u64_at(d, 137),
            total_budget: u64_at(d, 145),
            spent: u64_at(d, 153),
            window_secs: u64_at(d, 161) as i64,
            window_cap: u64_at(d, 169),
            window_start: u64_at(d, 177) as i64,
            window_spent: u64_at(d, 185),
            expires_at: u64_at(d, 193) as i64,
            created_at: u64_at(d, 201) as i64,
            pulls: u64_at(d, 209),
            last_intent,
            frozen: d[249] == 1,
            revoked: d[250] == 1,
            bump: d[251],
        })
    }

    pub fn write(&self, d: &mut [u8]) {
        d[0] = DISCRIMINATOR;
        d[1..33].copy_from_slice(self.owner.as_ref());
        d[33..65].copy_from_slice(self.agent.as_ref());
        d[65..97].copy_from_slice(self.mint.as_ref());
        d[97..129].copy_from_slice(self.destination.as_ref());
        let nums: [u64; 11] = [
            self.id,
            self.per_call_cap,
            self.total_budget,
            self.spent,
            self.window_secs as u64,
            self.window_cap,
            self.window_start as u64,
            self.window_spent,
            self.expires_at as u64,
            self.created_at as u64,
            self.pulls,
        ];
        for (i, v) in nums.iter().enumerate() {
            d[129 + i * 8..137 + i * 8].copy_from_slice(&v.to_le_bytes());
        }
        d[217..249].copy_from_slice(&self.last_intent);
        d[249] = self.frozen as u8;
        d[250] = self.revoked as u8;
        d[251] = self.bump;
    }

    /// Checks every limit for a pull of `amount` at `now` and, only if all of
    /// them hold, counts it. A refused pull changes nothing.
    pub fn record_pull(&mut self, now: i64, amount: u64, intent: [u8; 32]) -> Result<(), ProgramError> {
        if self.revoked {
            return Err(SpendPassError::PassRevoked.into());
        }
        if self.frozen {
            return Err(SpendPassError::PassFrozen.into());
        }
        if now >= self.expires_at {
            return Err(SpendPassError::PassExpired.into());
        }
        if amount == 0 {
            return Err(SpendPassError::ZeroAmount.into());
        }
        if amount > self.per_call_cap {
            return Err(SpendPassError::OverPerCallCap.into());
        }
        let spent = self.spent.checked_add(amount).ok_or(SpendPassError::Overflow)?;
        if spent > self.total_budget {
            return Err(SpendPassError::OverBudget.into());
        }
        let (window_start, window_spent) = if self.window_secs > 0 {
            let start = self.window_start_at(now)?;
            let before = if start == self.window_start { self.window_spent } else { 0 };
            let after = before.checked_add(amount).ok_or(SpendPassError::Overflow)?;
            if after > self.window_cap {
                return Err(SpendPassError::OverWindowCap.into());
            }
            (start, after)
        } else {
            (self.window_start, self.window_spent)
        };
        let pulls = self.pulls.checked_add(1).ok_or(SpendPassError::Overflow)?;
        self.spent = spent;
        self.window_start = window_start;
        self.window_spent = window_spent;
        self.pulls = pulls;
        self.last_intent = intent;
        Ok(())
    }

    /// Gives `amount` back to the budget, and to its window while that window
    /// is still the current one.
    pub fn record_refund(&mut self, now: i64, amount: u64) -> Result<(), ProgramError> {
        if amount == 0 {
            return Err(SpendPassError::ZeroAmount.into());
        }
        if amount > self.spent {
            return Err(SpendPassError::RefundOverSpent.into());
        }
        self.spent -= amount;
        if self.window_secs > 0 && self.window_start_at(now)? == self.window_start {
            self.window_spent = self.window_spent.saturating_sub(amount);
        }
        Ok(())
    }

    /// Windows are laid end to end from the pass's creation.
    fn window_start_at(&self, now: i64) -> Result<i64, ProgramError> {
        let elapsed = now.saturating_sub(self.created_at).max(0);
        let whole = elapsed / self.window_secs;
        let offset = whole.checked_mul(self.window_secs).ok_or(SpendPassError::Overflow)?;
        Ok(self.created_at.checked_add(offset).ok_or(SpendPassError::Overflow)?)
    }

    pub fn remaining_budget(&self) -> u64 {
        self.total_budget.saturating_sub(self.spent)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn pass() -> Pass {
        Pass {
            owner: Address::new_from_array([1; 32]),
            agent: Address::new_from_array([2; 32]),
            mint: Address::new_from_array([3; 32]),
            destination: Address::new_from_array([4; 32]),
            id: 1,
            per_call_cap: 100,
            total_budget: 1_000,
            spent: 0,
            window_secs: 0,
            window_cap: 0,
            window_start: 0,
            window_spent: 0,
            expires_at: 10_000,
            created_at: 1_000,
            pulls: 0,
            last_intent: [0; 32],
            frozen: false,
            revoked: false,
            bump: 255,
        }
    }

    fn windowed() -> Pass {
        Pass { window_secs: 60, window_cap: 150, ..pass() }
    }

    fn refused<T: core::fmt::Debug>(r: Result<T, ProgramError>, want: SpendPassError) {
        assert_eq!(r.expect_err("should have been refused"), ProgramError::Custom(want as u32));
    }

    #[test]
    fn the_layout_round_trips_at_the_anchor_offsets() {
        let mut p = windowed();
        (p.spent, p.pulls, p.frozen, p.last_intent) = (7, 3, true, [9; 32]);
        let mut d = [0u8; LEN];
        p.write(&mut d);
        assert_eq!((d[0], d[251], d[249], d[250]), (1, 255, 1, 0));
        assert_eq!(&d[1..33], &[1u8; 32]);
        assert_eq!(u64_at(&d, 129), 1); // id
        assert_eq!(u64_at(&d, 153), 7); // spent
        assert_eq!(u64_at(&d, 161), 60); // window_secs
        assert_eq!(Pass::read(&d).unwrap(), p);
        d[0] = 2;
        assert_eq!(Pass::read(&d), Err(ProgramError::InvalidAccountData));
    }

    #[test]
    fn the_budget_is_exact_to_the_last_unit() {
        let mut p = pass();
        for _ in 0..9 {
            p.record_pull(2_000, 100, [0; 32]).unwrap();
        }
        p.record_pull(2_000, 99, [0; 32]).unwrap();
        refused(p.record_pull(2_000, 2, [0; 32]), SpendPassError::OverBudget);
        p.record_pull(2_000, 1, [0; 32]).unwrap();
        assert_eq!(p.spent, 1_000);
    }

    #[test]
    fn every_refusal_leaves_no_trace() {
        let mut p = pass();
        p.record_pull(2_000, 50, [1; 32]).unwrap();
        let before = p;
        refused(p.record_pull(2_000, 0, [2; 32]), SpendPassError::ZeroAmount);
        refused(p.record_pull(2_000, 101, [2; 32]), SpendPassError::OverPerCallCap);
        refused(p.record_pull(10_000, 1, [2; 32]), SpendPassError::PassExpired);
        p.frozen = true;
        refused(p.record_pull(2_000, 1, [2; 32]), SpendPassError::PassFrozen);
        p.frozen = false;
        p.revoked = true;
        refused(p.record_pull(2_000, 1, [2; 32]), SpendPassError::PassRevoked);
        p.revoked = false;
        assert_eq!(p, before);
    }

    #[test]
    fn windows_cap_and_reopen_and_cannot_be_restarted_late() {
        let mut p = windowed();
        p.record_pull(1_059, 100, [0; 32]).unwrap();
        p.record_pull(1_064, 100, [0; 32]).unwrap();
        assert_eq!(p.window_start, 1_060);
        refused(p.record_pull(1_065, 51, [0; 32]), SpendPassError::OverWindowCap);
        p.record_pull(1_500, 100, [0; 32]).unwrap();
        assert_eq!((p.window_start, p.window_spent), (1_480, 100));
    }

    #[test]
    fn refunds_are_bounded_and_free_only_their_own_window() {
        let mut p = windowed();
        p.record_pull(1_010, 100, [0; 32]).unwrap();
        refused(p.record_refund(1_020, 101), SpendPassError::RefundOverSpent);
        refused(p.record_refund(1_020, 0), SpendPassError::ZeroAmount);
        p.record_refund(1_020, 70).unwrap();
        assert_eq!((p.spent, p.window_spent), (30, 30));
        p.record_pull(1_030, 100, [0; 32]).unwrap();
        p.record_refund(1_070, 50).unwrap();
        assert_eq!((p.spent, p.window_spent, p.window_start), (80, 130, 1_000));
    }

    #[test]
    fn overflow_is_an_error_not_a_panic() {
        let mut p = Pass { per_call_cap: u64::MAX, total_budget: u64::MAX, spent: u64::MAX - 5, ..pass() };
        refused(p.record_pull(2_000, 6, [0; 32]), SpendPassError::Overflow);
        p.record_pull(2_000, 5, [0; 32]).unwrap();
    }

    #[test]
    fn terms_must_make_sense() {
        let ok = Terms { per_call_cap: 10, total_budget: 100, window_secs: 0, window_cap: 0, expires_at: 500 };
        ok.validate(100).unwrap();
        for bad in [
            Terms { per_call_cap: 0, ..ok },
            Terms { total_budget: 9, ..ok },
            Terms { expires_at: 100, ..ok },
            Terms { window_cap: 5, ..ok },
            Terms { window_secs: -1, window_cap: 10, ..ok },
            Terms { window_secs: 60, window_cap: 9, ..ok },
        ] {
            assert_eq!(bad.validate(100), Err(ProgramError::Custom(SpendPassError::InvalidTerms as u32)));
        }
    }
}
