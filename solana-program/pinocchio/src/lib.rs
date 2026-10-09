//! Algebra Spend Pass on Pinocchio: the Anchor program in
//! `programs/spend-pass`, rewritten without a framework to make it much
//! smaller (rent is paid per byte). It takes the same instruction bytes and
//! accounts in the same order, keeps the pass in the same layout, returns the
//! same error numbers and logs the same events, so anything that speaks to one
//! speaks to the other; only the program id differs.
//!
//! Anchor checked the accounts from attributes. Here every check is written
//! out, next to the instruction it guards, and named after the Anchor
//! constraint it replaces.

#![cfg_attr(not(test), no_std)]

pub mod error;
pub mod state;

use pinocchio::{
    cpi::{Seed, Signer},
    error::ProgramError,
    sysvars::{clock::Clock, get_sysvar, rent::RENT_ID, Sysvar},
    AccountView, Address, ProgramResult,
};
use pinocchio_token::{
    instructions::{CloseAccount, TransferChecked},
    state::{Account as TokenAccount, Mint},
};

use crate::{
    error::SpendPassError,
    state::{key_at, u64_at, Pass, Terms, LEN, SEED},
};

pub const ID: Address = Address::from_str_const("F1Uu8ynQUviLZHUgejHDMkKmFAJJ7ZKDwU3Svc7Xpj21");

#[cfg(all(target_os = "solana", not(feature = "no-entrypoint")))]
mod entry {
    pinocchio::program_entrypoint!(crate::process_instruction);
    pinocchio::no_allocator!();
    pinocchio::nostd_panic_handler!();
}

pub fn process_instruction(program_id: &Address, accounts: &mut [AccountView], data: &[u8]) -> ProgramResult {
    let (disc, args) = data.split_first().ok_or(ProgramError::InvalidInstructionData)?;
    match disc {
        0 => create_pass(program_id, accounts, args),
        1 => deposit(program_id, accounts, args),
        2 => pull(program_id, accounts, args),
        3 | 4 | 6 => manage(program_id, accounts, *disc, args),
        5 => withdraw(program_id, accounts, args),
        7 => close_pass(program_id, accounts),
        8 => refund(program_id, accounts, args),
        _ => Err(ProgramError::InvalidInstructionData),
    }
}

// --- account checks -------------------------------------------------------

fn take<const N: usize>(accounts: &[AccountView]) -> Result<[AccountView; N], ProgramError> {
    if accounts.len() < N {
        return Err(ProgramError::NotEnoughAccountKeys);
    }
    Ok(core::array::from_fn(|i| accounts[i]))
}

/// `Signer<'info>`
fn signer(a: &AccountView) -> ProgramResult {
    if a.is_signer() {
        Ok(())
    } else {
        Err(ProgramError::MissingRequiredSignature)
    }
}

/// `#[account(mut)]`
fn writable(a: &AccountView) -> ProgramResult {
    if a.is_writable() {
        Ok(())
    } else {
        Err(ProgramError::Immutable)
    }
}

/// `has_one = …`: the pass names this account.
fn has_one(named: &Address, a: &AccountView) -> ProgramResult {
    if named == a.address() {
        Ok(())
    } else {
        Err(ProgramError::IncorrectAuthority)
    }
}

/// `Program<'info, Token>`
fn token_program(a: &AccountView) -> ProgramResult {
    if a.address() == &pinocchio_token::ID {
        Ok(())
    } else {
        Err(ProgramError::IncorrectProgramId)
    }
}

/// `Account<'info, Pass>` with `seeds = [b"pass", owner, id], bump = pass.bump`:
/// owned by this program, a pass, at the address its own seeds and bump give.
fn load_pass(program_id: &Address, a: &AccountView) -> Result<Pass, ProgramError> {
    if !a.owned_by(program_id) {
        return Err(ProgramError::IllegalOwner);
    }
    let p = Pass::read(&a.try_borrow()?)?;
    let id = p.id.to_le_bytes();
    let expected = Address::derive_address(&[SEED, p.owner.as_ref(), &id[..]], Some(p.bump), program_id);
    if a.address() != &expected {
        return Err(ProgramError::InvalidSeeds);
    }
    Ok(p)
}

fn store_pass(a: &mut AccountView, p: &Pass) -> ProgramResult {
    p.write(&mut a.try_borrow_mut()?);
    Ok(())
}

/// `associated_token::mint = mint, associated_token::authority = pass`: the
/// vault is the pass's associated token account for the mint.
fn vault_of(pass: &Address, mint: &Address, vault: &AccountView) -> ProgramResult {
    let (ata, _) = Address::find_program_address(
        &[pass.as_ref(), pinocchio_token::ID.as_ref(), mint.as_ref()],
        &pinocchio_associated_token_account::ID,
    );
    if vault.address() != &ata {
        return Err(ProgramError::InvalidSeeds);
    }
    TokenAccount::from_account_view(vault)?;
    Ok(())
}

/// `token::mint = mint, token::authority = authority` (authority optional).
fn token_account(a: &AccountView, mint: &Address, authority: Option<&Address>) -> ProgramResult {
    let t = TokenAccount::from_account_view(a)?;
    if t.mint() != mint {
        return Err(ProgramError::InvalidAccountData);
    }
    if let Some(auth) = authority {
        if t.owner() != auth {
            return Err(ProgramError::IncorrectAuthority);
        }
    }
    Ok(())
}

fn decimals(mint: &AccountView) -> Result<u8, ProgramError> {
    Ok(Mint::from_account_view(mint)?.decimals())
}

/// The rent-exempt minimum for `len` bytes, read from the rent sysvar's own
/// fields. Pinocchio's `Rent` reads only the first field and assumes it is
/// already "lamports per byte" (the newer rent model); on a cluster still on
/// the classic model (lamports per byte-year, times an exemption threshold of
/// 2.0) that is half the real minimum, and the account would be refused.
/// This works under both.
fn rent_exempt_minimum(len: usize) -> Result<u64, ProgramError> {
    let mut raw = [0u8; 16];
    get_sysvar(&mut raw, &RENT_ID, 0)?;
    let per_byte = u64_at(&raw, 0);
    let threshold = f64::from_bits(u64_at(&raw, 8));
    let base = (128 + len as u64).checked_mul(per_byte).ok_or(SpendPassError::Overflow)?;
    if threshold == 2.0 {
        return Ok(base.checked_mul(2).ok_or(SpendPassError::Overflow)?);
    }
    if threshold == 1.0 || threshold == 0.0 {
        return Ok(base);
    }
    Ok((base as f64 * threshold) as u64)
}

fn now() -> Result<i64, ProgramError> {
    Ok(Clock::get()?.unix_timestamp)
}

fn amount_arg(args: &[u8]) -> Result<u64, ProgramError> {
    if args.len() < 8 {
        return Err(ProgramError::InvalidInstructionData);
    }
    Ok(u64_at(args, 0))
}

fn intent_arg(args: &[u8]) -> Result<[u8; 32], ProgramError> {
    if args.len() < 40 {
        return Err(ProgramError::InvalidInstructionData);
    }
    let mut i = [0u8; 32];
    i.copy_from_slice(&args[8..40]);
    Ok(i)
}

// --- events: what Anchor's emit! logs (sha256("event:<Name>")[..8] + Borsh) ---

const EV_PASS_CREATED: [u8; 8] = [7, 218, 108, 191, 58, 181, 157, 48];
const EV_DEPOSITED: [u8; 8] = [111, 141, 26, 45, 161, 35, 100, 57];
const EV_PULLED: [u8; 8] = [65, 228, 151, 236, 209, 242, 90, 236];
const EV_REFUNDED: [u8; 8] = [35, 103, 149, 246, 196, 123, 221, 99];
const EV_FROZEN_CHANGED: [u8; 8] = [223, 64, 57, 47, 168, 188, 36, 90];
const EV_AGENT_CHANGED: [u8; 8] = [248, 6, 169, 87, 197, 255, 16, 191];
const EV_WITHDRAWN: [u8; 8] = [20, 89, 223, 198, 194, 124, 219, 13];
const EV_REVOKED: [u8; 8] = [113, 216, 148, 99, 124, 184, 0, 65];
const EV_CLOSED: [u8; 8] = [50, 31, 87, 155, 135, 220, 195, 239];

/// Builds an event in a stack buffer and logs it with sol_log_data.
struct Event {
    buf: [u8; 256],
    len: usize,
}

impl Event {
    fn new(disc: [u8; 8]) -> Self {
        let mut e = Self { buf: [0; 256], len: 8 };
        e.buf[..8].copy_from_slice(&disc);
        e
    }
    fn bytes(mut self, b: &[u8]) -> Self {
        self.buf[self.len..self.len + b.len()].copy_from_slice(b);
        self.len += b.len();
        self
    }
    fn key(self, k: &Address) -> Self {
        self.bytes(k.as_ref())
    }
    fn u64(self, v: u64) -> Self {
        self.bytes(&v.to_le_bytes())
    }
    fn emit(self) {
        #[cfg(target_os = "solana")]
        {
            let data: &[u8] = &self.buf[..self.len];
            let slices: [&[u8]; 1] = [data];
            // SAFETY: a pointer to one (ptr, len) slice, as the syscall expects.
            unsafe { pinocchio::syscalls::sol_log_data(slices.as_ptr() as *const u8, 1) };
        }
        #[cfg(not(target_os = "solana"))]
        let _ = self.len;
    }
}

// --- instructions ---------------------------------------------------------

/// Accounts: owner (signer, mut), agent, mint, destination, pass (mut),
/// vault (mut), associated token program, token program, system program.
/// Data: id u64, terms.
fn create_pass(program_id: &Address, accounts: &mut [AccountView], args: &[u8]) -> ProgramResult {
    let [owner, agent, mint, destination, mut pass, vault, ata_program, tok_program, system_program] = take::<9>(accounts)?;
    signer(&owner)?;
    writable(&owner)?;
    writable(&pass)?;
    writable(&vault)?;
    if args.len() < 8 + Terms::LEN {
        return Err(ProgramError::InvalidInstructionData);
    }
    let id = u64_at(args, 0);
    let terms = Terms::read(&args[8..])?;
    let now = now()?;
    terms.validate(now)?;
    if agent.address() == owner.address() || agent.address() == &Address::default() {
        return Err(SpendPassError::InvalidAgent.into());
    }
    if ata_program.address() != &pinocchio_associated_token_account::ID || system_program.address() != &pinocchio_system::ID {
        return Err(ProgramError::IncorrectProgramId);
    }
    token_program(&tok_program)?;
    decimals(&mint)?; // a real mint of the token program
    token_account(&destination, mint.address(), None)?;

    let id_bytes = id.to_le_bytes();
    let (expected, bump) = Address::find_program_address(&[SEED, owner.address().as_ref(), &id_bytes], program_id);
    if pass.address() != &expected {
        return Err(ProgramError::InvalidSeeds);
    }
    let (ata, _) = Address::find_program_address(
        &[expected.as_ref(), pinocchio_token::ID.as_ref(), mint.address().as_ref()],
        &pinocchio_associated_token_account::ID,
    );
    if vault.address() != &ata {
        return Err(ProgramError::InvalidSeeds);
    }
    {
        let dest = TokenAccount::from_account_view(&destination)?;
        if destination.address() == vault.address() || dest.owner() == pass.address() {
            return Err(SpendPassError::InvalidDestination.into());
        }
    }

    // `init`: make the pass account, even if someone sent lamports to its
    // address first (which would make a plain CreateAccount fail).
    if pass.owned_by(program_id) || !pass.is_data_empty() {
        return Err(ProgramError::AccountAlreadyInitialized);
    }
    let bump_seed = [bump];
    let seeds = [Seed::from(SEED), Seed::from(owner.address().as_ref()), Seed::from(&id_bytes), Seed::from(&bump_seed)];
    let pass_signer = [Signer::from(&seeds)];
    let rent = rent_exempt_minimum(LEN)?;
    if pass.lamports() == 0 {
        pinocchio_system::instructions::CreateAccount { from: &owner, to: &pass, lamports: rent, space: LEN as u64, owner: program_id }
            .invoke_signed(&pass_signer)?;
    } else {
        let short = rent.saturating_sub(pass.lamports());
        if short > 0 {
            pinocchio_system::instructions::Transfer { from: &owner, to: &pass, lamports: short }.invoke()?;
        }
        pinocchio_system::instructions::Allocate { account: &pass, space: LEN as u64 }.invoke_signed(&pass_signer)?;
        pinocchio_system::instructions::Assign { account: &pass, owner: program_id }.invoke_signed(&pass_signer)?;
    }
    // The vault: the pass's associated token account, which only this program
    // can move tokens out of.
    pinocchio_associated_token_account::instructions::CreateIdempotent {
        funding_account: &owner,
        account: &vault,
        wallet: &pass,
        mint: &mint,
        system_program: &system_program,
        token_program: &tok_program,
    }
    .invoke()?;

    let p = Pass {
        owner: *owner.address(),
        agent: *agent.address(),
        mint: *mint.address(),
        destination: *destination.address(),
        id,
        per_call_cap: terms.per_call_cap,
        total_budget: terms.total_budget,
        spent: 0,
        window_secs: terms.window_secs,
        window_cap: terms.window_cap,
        window_start: now,
        window_spent: 0,
        expires_at: terms.expires_at,
        created_at: now,
        pulls: 0,
        last_intent: [0; 32],
        frozen: false,
        revoked: false,
        bump,
    };
    store_pass(&mut pass, &p)?;
    Event::new(EV_PASS_CREATED)
        .key(pass.address())
        .key(owner.address())
        .key(agent.address())
        .key(mint.address())
        .key(destination.address())
        .u64(terms.per_call_cap)
        .u64(terms.total_budget)
        .u64(terms.window_secs as u64)
        .u64(terms.window_cap)
        .u64(terms.expires_at as u64)
        .emit();
    Ok(())
}

/// Accounts: owner (signer), pass, mint, owner token (mut), vault (mut), token program.
fn deposit(program_id: &Address, accounts: &mut [AccountView], args: &[u8]) -> ProgramResult {
    let [owner, pass, mint, owner_token, vault, tok_program] = take::<6>(accounts)?;
    signer(&owner)?;
    let p = load_pass(program_id, &pass)?;
    has_one(&p.owner, &owner)?;
    has_one(&p.mint, &mint)?;
    let decimals = decimals(&mint)?;
    token_account(&owner_token, mint.address(), Some(owner.address()))?;
    vault_of(pass.address(), mint.address(), &vault)?;
    token_program(&tok_program)?;
    let amount = amount_arg(args)?;
    if p.revoked {
        return Err(SpendPassError::PassRevoked.into());
    }
    if amount == 0 {
        return Err(SpendPassError::ZeroAmount.into());
    }
    TransferChecked::new(&owner_token, &mint, &vault, &owner, amount, decimals).invoke()?;
    Event::new(EV_DEPOSITED).key(pass.address()).u64(amount).emit();
    Ok(())
}

/// Accounts: agent (signer), pass (mut), mint, vault (mut), destination (mut), token program.
/// Data: amount u64, intent [u8; 32].
fn pull(program_id: &Address, accounts: &mut [AccountView], args: &[u8]) -> ProgramResult {
    let [agent, mut pass, mint, vault, destination, tok_program] = take::<6>(accounts)?;
    signer(&agent)?;
    writable(&pass)?;
    let mut p = load_pass(program_id, &pass)?;
    has_one(&p.agent, &agent)?;
    has_one(&p.mint, &mint)?;
    has_one(&p.destination, &destination)?;
    let decimals = decimals(&mint)?;
    vault_of(pass.address(), mint.address(), &vault)?;
    token_account(&destination, mint.address(), None)?;
    token_program(&tok_program)?;
    let amount = amount_arg(args)?;
    let intent = intent_arg(args)?;
    p.record_pull(now()?, amount, intent)?;
    store_pass(&mut pass, &p)?;

    let id = p.id.to_le_bytes();
    let bump = [p.bump];
    let seeds = [Seed::from(SEED), Seed::from(p.owner.as_ref()), Seed::from(&id), Seed::from(&bump)];
    TransferChecked::new(&vault, &mint, &destination, &pass, amount, decimals).invoke_signed(&[Signer::from(&seeds)])?;
    Event::new(EV_PULLED)
        .key(pass.address())
        .key(agent.address())
        .u64(amount)
        .bytes(&intent)
        .u64(p.spent)
        .u64(p.remaining_budget())
        .emit();
    Ok(())
}

/// Accounts: owner (signer), pass (mut). 3 set_frozen(bool), 4 set_agent(key), 6 revoke.
fn manage(program_id: &Address, accounts: &mut [AccountView], disc: u8, args: &[u8]) -> ProgramResult {
    let [owner, mut pass] = take::<2>(accounts)?;
    signer(&owner)?;
    writable(&pass)?;
    let mut p = load_pass(program_id, &pass)?;
    has_one(&p.owner, &owner)?;
    match disc {
        3 => {
            let frozen = match args.first() {
                Some(0) => false,
                Some(1) => true,
                _ => return Err(ProgramError::InvalidInstructionData),
            };
            p.frozen = frozen;
            store_pass(&mut pass, &p)?;
            Event::new(EV_FROZEN_CHANGED).key(pass.address()).bytes(&[frozen as u8]).emit();
        }
        4 => {
            if args.len() < 32 {
                return Err(ProgramError::InvalidInstructionData);
            }
            let agent = key_at(args, 0);
            if &agent == owner.address() || agent == Address::default() {
                return Err(SpendPassError::InvalidAgent.into());
            }
            p.agent = agent;
            store_pass(&mut pass, &p)?;
            Event::new(EV_AGENT_CHANGED).key(pass.address()).key(&agent).emit();
        }
        _ => {
            if p.revoked {
                return Err(SpendPassError::AlreadyRevoked.into());
            }
            p.revoked = true;
            store_pass(&mut pass, &p)?;
            Event::new(EV_REVOKED).key(pass.address()).emit();
        }
    }
    Ok(())
}

/// Accounts: owner (signer), pass, mint, owner token (mut), vault (mut), token program.
fn withdraw(program_id: &Address, accounts: &mut [AccountView], args: &[u8]) -> ProgramResult {
    let [owner, pass, mint, owner_token, vault, tok_program] = take::<6>(accounts)?;
    signer(&owner)?;
    let p = load_pass(program_id, &pass)?;
    has_one(&p.owner, &owner)?;
    has_one(&p.mint, &mint)?;
    let decimals = decimals(&mint)?;
    token_account(&owner_token, mint.address(), Some(owner.address()))?;
    vault_of(pass.address(), mint.address(), &vault)?;
    token_program(&tok_program)?;
    let amount = amount_arg(args)?;
    if amount == 0 {
        return Err(SpendPassError::ZeroAmount.into());
    }
    let id = p.id.to_le_bytes();
    let bump = [p.bump];
    let seeds = [Seed::from(SEED), Seed::from(p.owner.as_ref()), Seed::from(&id), Seed::from(&bump)];
    TransferChecked::new(&vault, &mint, &owner_token, &pass, amount, decimals).invoke_signed(&[Signer::from(&seeds)])?;
    Event::new(EV_WITHDRAWN).key(pass.address()).u64(amount).emit();
    Ok(())
}

/// Accounts: owner (signer, mut), pass (mut), mint, owner token (mut), vault (mut), token program.
fn close_pass(program_id: &Address, accounts: &mut [AccountView]) -> ProgramResult {
    let [mut owner, mut pass, mint, owner_token, vault, tok_program] = take::<6>(accounts)?;
    signer(&owner)?;
    writable(&owner)?;
    writable(&pass)?;
    let p = load_pass(program_id, &pass)?;
    has_one(&p.owner, &owner)?;
    has_one(&p.mint, &mint)?;
    let decimals = decimals(&mint)?;
    token_account(&owner_token, mint.address(), Some(owner.address()))?;
    vault_of(pass.address(), mint.address(), &vault)?;
    token_program(&tok_program)?;

    let id = p.id.to_le_bytes();
    let bump = [p.bump];
    let seeds = [Seed::from(SEED), Seed::from(p.owner.as_ref()), Seed::from(&id), Seed::from(&bump)];
    let returned = TokenAccount::from_account_view(&vault)?.amount();
    if returned > 0 {
        TransferChecked::new(&vault, &mint, &owner_token, &pass, returned, decimals).invoke_signed(&[Signer::from(&seeds)])?;
    }
    CloseAccount::new(&vault, &owner, &pass).invoke_signed(&[Signer::from(&seeds)])?;
    // `close = owner`: the pass's rent back to the owner, the account gone.
    let lamports = pass.lamports();
    owner.set_lamports(owner.lamports().checked_add(lamports).ok_or(SpendPassError::Overflow)?);
    pass.set_lamports(0);
    pass.close()?;
    Event::new(EV_CLOSED).key(pass.address()).u64(returned).emit();
    Ok(())
}

/// Accounts: authority (signer), pass (mut), mint, vault (mut), destination (mut), token program.
/// Data: amount u64, intent [u8; 32].
fn refund(program_id: &Address, accounts: &mut [AccountView], args: &[u8]) -> ProgramResult {
    let [authority, mut pass, mint, vault, destination, tok_program] = take::<6>(accounts)?;
    signer(&authority)?;
    writable(&pass)?;
    let mut p = load_pass(program_id, &pass)?;
    has_one(&p.mint, &mint)?;
    has_one(&p.destination, &destination)?;
    let decimals = decimals(&mint)?;
    vault_of(pass.address(), mint.address(), &vault)?;
    token_account(&destination, mint.address(), Some(authority.address()))?;
    token_program(&tok_program)?;
    let amount = amount_arg(args)?;
    let intent = intent_arg(args)?;
    p.record_refund(now()?, amount)?;
    store_pass(&mut pass, &p)?;
    TransferChecked::new(&destination, &mint, &vault, &authority, amount, decimals).invoke()?;
    Event::new(EV_REFUNDED)
        .key(pass.address())
        .u64(amount)
        .bytes(&intent)
        .u64(p.spent)
        .u64(p.remaining_budget())
        .emit();
    Ok(())
}
