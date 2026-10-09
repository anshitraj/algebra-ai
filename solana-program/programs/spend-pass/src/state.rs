use anchor_lang::prelude::*;

use crate::error::SpendPassError;

/// What an owner lets an agent do, fixed when the pass is made. The owner can
/// freeze, revoke or withdraw later, but can't loosen a pass the agent already
/// holds: to allow more, make another pass.
#[derive(AnchorSerialize, AnchorDeserialize, Clone, Copy, Debug, PartialEq, Eq)]
pub struct PassTerms {
    /// The most one pull may take.
    pub per_call_cap: u64,
    /// The most all pulls together may ever take.
    pub total_budget: u64,
    /// Length of one spending window in seconds, counted from the pass's
    /// creation. 0 means no window cap.
    pub window_secs: i64,
    /// The most pulls may take within one window. Must be 0 when
    /// `window_secs` is 0.
    pub window_cap: u64,
    /// Unix time at and after which pulls stop.
    pub expires_at: i64,
}

impl PassTerms {
    pub fn validate(&self, now: i64) -> Result<()> {
        require!(self.per_call_cap > 0, SpendPassError::InvalidTerms);
        require!(self.total_budget >= self.per_call_cap, SpendPassError::InvalidTerms);
        require!(self.expires_at > now, SpendPassError::InvalidTerms);
        if self.window_secs == 0 {
            require!(self.window_cap == 0, SpendPassError::InvalidTerms);
        } else {
            require!(self.window_secs > 0, SpendPassError::InvalidTerms);
            require!(self.window_cap >= self.per_call_cap, SpendPassError::InvalidTerms);
        }
        Ok(())
    }
}

/// One pass: a budget, and the key allowed to spend it. The tokens themselves
/// sit in the pass's vault, an associated token account the pass owns, so only
/// this program can move them.
#[derive(InitSpace)]
#[account(discriminator = 1)]
pub struct Pass {
    /// The person. Can freeze, revoke, withdraw, rotate the agent and close.
    pub owner: Pubkey,
    /// The key that may pull, and nothing else.
    pub agent: Pubkey,
    pub mint: Pubkey,
    /// The one token account a pull is paid to.
    pub destination: Pubkey,
    /// Chosen by the owner; with the owner it makes the pass's address.
    pub id: u64,
    pub per_call_cap: u64,
    pub total_budget: u64,
    pub spent: u64,
    pub window_secs: i64,
    pub window_cap: u64,
    /// Start of the window `window_spent` was counted in.
    pub window_start: i64,
    pub window_spent: u64,
    pub expires_at: i64,
    pub created_at: i64,
    pub pulls: u64,
    /// The request the latest pull paid for.
    pub last_intent: [u8; 32],
    pub frozen: bool,
    pub revoked: bool,
    pub bump: u8,
}

impl Pass {
    pub const SEED: &'static [u8] = b"pass";

    /// Checks every limit for a pull of `amount` at time `now` and, only if all
    /// of them hold, counts it. A refused pull changes nothing.
    pub fn record_pull(&mut self, now: i64, amount: u64, intent: [u8; 32]) -> Result<()> {
        require!(!self.revoked, SpendPassError::PassRevoked);
        require!(!self.frozen, SpendPassError::PassFrozen);
        require!(now < self.expires_at, SpendPassError::PassExpired);
        require!(amount > 0, SpendPassError::ZeroAmount);
        require!(amount <= self.per_call_cap, SpendPassError::OverPerCallCap);
        let spent = self.spent.checked_add(amount).ok_or(SpendPassError::Overflow)?;
        require!(spent <= self.total_budget, SpendPassError::OverBudget);

        let (window_start, window_spent) = if self.window_secs > 0 {
            let start = self.window_start_at(now)?;
            let before = if start == self.window_start { self.window_spent } else { 0 };
            let after = before.checked_add(amount).ok_or(SpendPassError::Overflow)?;
            require!(after <= self.window_cap, SpendPassError::OverWindowCap);
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

    /// Windows are laid end to end from the pass's creation, so a window can't
    /// be restarted early by pulling at a chosen moment.
    fn window_start_at(&self, now: i64) -> Result<i64> {
        let elapsed = now.saturating_sub(self.created_at).max(0);
        let whole = elapsed / self.window_secs;
        let offset = whole.checked_mul(self.window_secs).ok_or(SpendPassError::Overflow)?;
        Ok(self.created_at.checked_add(offset).ok_or(SpendPassError::Overflow)?)
    }

    /// Gives `amount` back to the budget. In the window it was pulled in, it is
    /// given back to that window too; once the window has passed there is
    /// nothing to give back there.
    pub fn record_refund(&mut self, now: i64, amount: u64) -> Result<()> {
        require!(amount > 0, SpendPassError::ZeroAmount);
        require!(amount <= self.spent, SpendPassError::RefundOverSpent);
        self.spent -= amount;
        if self.window_secs > 0 && self.window_start_at(now)? == self.window_start {
            self.window_spent = self.window_spent.saturating_sub(amount);
        }
        Ok(())
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
            owner: Pubkey::new_unique(),
            agent: Pubkey::new_unique(),
            mint: Pubkey::new_unique(),
            destination: Pubkey::new_unique(),
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

    fn code(e: anchor_lang::error::Error) -> u32 {
        match e {
            anchor_lang::error::Error::AnchorError(a) => a.error_code_number,
            other => panic!("not an anchor error: {other:?}"),
        }
    }

    fn refused<T: std::fmt::Debug>(r: Result<T>, want: SpendPassError) {
        let want: u32 = want.into();
        assert_eq!(code(r.expect_err("should have been refused")), want);
    }

    #[test]
    fn a_pull_inside_every_limit_is_counted() {
        let mut p = pass();
        p.record_pull(2_000, 100, [7; 32]).unwrap();
        assert_eq!((p.spent, p.pulls, p.last_intent), (100, 1, [7; 32]));
        assert_eq!(p.remaining_budget(), 900);
    }

    #[test]
    fn zero_and_over_the_cap_are_refused() {
        let mut p = pass();
        refused(p.record_pull(2_000, 0, [0; 32]), SpendPassError::ZeroAmount);
        refused(p.record_pull(2_000, 101, [0; 32]), SpendPassError::OverPerCallCap);
        assert_eq!((p.spent, p.pulls), (0, 0));
    }

    #[test]
    fn the_budget_is_exact_to_the_last_unit() {
        let mut p = pass();
        for _ in 0..9 {
            p.record_pull(2_000, 100, [0; 32]).unwrap();
        }
        p.record_pull(2_000, 99, [0; 32]).unwrap(); // 999 spent
        refused(p.record_pull(2_000, 2, [0; 32]), SpendPassError::OverBudget);
        p.record_pull(2_000, 1, [0; 32]).unwrap(); // exactly 1,000
        refused(p.record_pull(2_000, 1, [0; 32]), SpendPassError::OverBudget);
        assert_eq!(p.spent, 1_000);
    }

    #[test]
    fn a_refused_pull_leaves_no_trace() {
        let mut p = pass();
        p.record_pull(2_000, 50, [1; 32]).unwrap();
        let before = (p.spent, p.pulls, p.last_intent, p.window_spent);
        let _ = p.record_pull(2_000, 101, [2; 32]);
        assert_eq!(before, (p.spent, p.pulls, p.last_intent, p.window_spent));
    }

    #[test]
    fn frozen_revoked_and_expired_passes_pay_nothing() {
        let mut p = pass();
        p.frozen = true;
        refused(p.record_pull(2_000, 1, [0; 32]), SpendPassError::PassFrozen);
        p.frozen = false;
        p.revoked = true;
        refused(p.record_pull(2_000, 1, [0; 32]), SpendPassError::PassRevoked);
        p.revoked = false;
        refused(p.record_pull(10_000, 1, [0; 32]), SpendPassError::PassExpired); // at expiry
        p.record_pull(9_999, 1, [0; 32]).unwrap(); // one second earlier
    }

    #[test]
    fn a_window_caps_what_can_be_taken_in_it_and_reopens_on_the_next() {
        let mut p = windowed(); // 60 s windows from t=1000, 150 a window, 100 a pull
        p.record_pull(1_010, 100, [0; 32]).unwrap();
        refused(p.record_pull(1_059, 51, [0; 32]), SpendPassError::OverWindowCap);
        p.record_pull(1_059, 50, [0; 32]).unwrap(); // the window is now full
        refused(p.record_pull(1_059, 1, [0; 32]), SpendPassError::OverWindowCap);
        p.record_pull(1_060, 100, [0; 32]).unwrap(); // the next window began at t=1060
        assert_eq!((p.spent, p.window_spent, p.window_start), (250, 100, 1_060));
    }

    #[test]
    fn windows_cannot_be_restarted_by_pulling_late() {
        let mut p = windowed();
        p.record_pull(1_059, 100, [0; 32]).unwrap();
        // 5 s into the next window the old spend no longer counts, but the new
        // window's start is still 1,060, not 1,064.
        p.record_pull(1_064, 100, [0; 32]).unwrap();
        assert_eq!(p.window_start, 1_060);
        refused(p.record_pull(1_065, 51, [0; 32]), SpendPassError::OverWindowCap);
    }

    #[test]
    fn a_skipped_window_starts_fresh() {
        let mut p = windowed();
        p.record_pull(1_010, 100, [0; 32]).unwrap();
        p.record_pull(1_500, 100, [0; 32]).unwrap(); // many windows later
        assert_eq!((p.window_start, p.window_spent), (1_480, 100));
    }

    #[test]
    fn arithmetic_that_would_overflow_is_an_error_not_a_panic() {
        let mut p = Pass { per_call_cap: u64::MAX, total_budget: u64::MAX, spent: u64::MAX - 5, ..pass() };
        refused(p.record_pull(2_000, 6, [0; 32]), SpendPassError::Overflow);
        p.record_pull(2_000, 5, [0; 32]).unwrap();
        assert_eq!(p.spent, u64::MAX);
        let mut q = Pass { window_secs: 1, window_cap: u64::MAX, per_call_cap: u64::MAX, total_budget: u64::MAX, window_spent: u64::MAX - 1, window_start: 1_000, ..pass() };
        refused(q.record_pull(1_000, 2, [0; 32]), SpendPassError::Overflow);
    }

    #[test]
    fn a_refund_gives_back_budget_and_never_more_than_was_spent() {
        let mut p = pass();
        p.record_pull(2_000, 100, [0; 32]).unwrap();
        refused(p.record_refund(2_000, 0), SpendPassError::ZeroAmount);
        refused(p.record_refund(2_000, 101), SpendPassError::RefundOverSpent);
        p.record_refund(2_000, 60).unwrap();
        assert_eq!((p.spent, p.remaining_budget()), (40, 960));
        p.record_refund(2_000, 40).unwrap();
        refused(p.record_refund(2_000, 1), SpendPassError::RefundOverSpent);
    }

    #[test]
    fn a_refund_frees_its_own_window_only() {
        let mut p = windowed(); // 60 s windows from t=1000, 150 a window
        p.record_pull(1_010, 100, [0; 32]).unwrap();
        p.record_refund(1_020, 70).unwrap();
        assert_eq!((p.spent, p.window_spent), (30, 30));
        p.record_pull(1_030, 100, [0; 32]).unwrap(); // 130 in this window: fits again
        // Refunded in the next window: the budget comes back, the new window
        // owes nothing.
        p.record_refund(1_070, 50).unwrap();
        assert_eq!((p.spent, p.window_spent, p.window_start), (80, 130, 1_000));
        p.record_pull(1_070, 100, [0; 32]).unwrap();
        assert_eq!((p.window_start, p.window_spent), (1_060, 100));
    }

    #[test]
    fn terms_must_make_sense() {
        let ok = PassTerms { per_call_cap: 10, total_budget: 100, window_secs: 0, window_cap: 0, expires_at: 500 };
        ok.validate(100).unwrap();
        for bad in [
            PassTerms { per_call_cap: 0, ..ok },
            PassTerms { total_budget: 9, ..ok },
            PassTerms { expires_at: 100, ..ok },
            PassTerms { window_cap: 5, ..ok },
            PassTerms { window_secs: -1, window_cap: 10, ..ok },
            PassTerms { window_secs: 60, window_cap: 9, ..ok },
        ] {
            refused(bad.validate(100), SpendPassError::InvalidTerms);
        }
        PassTerms { window_secs: 60, window_cap: 10, ..ok }.validate(100).unwrap();
    }
}
