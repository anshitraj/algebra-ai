//! Runs the built program (target/deploy/spend_pass.so) in LiteSVM: every
//! instruction, and every way a pull or refund must be refused. Build first
//! with `cargo build-sbf`.

use {
    anchor_lang::{
        prelude::Clock, solana_program::instruction::Instruction,
        solana_program::program_pack::Pack, system_program::ID as SYSTEM_PROGRAM_ID,
        AccountDeserialize, InstructionData, ToAccountMetas,
    },
    anchor_spl::{
        associated_token::{self, ID as ASSOCIATED_TOKEN_PROGRAM_ID},
        token::spl_token,
    },
    litesvm::LiteSVM,
    litesvm_token::{
        spl_token::ID as TOKEN_PROGRAM_ID, CreateAssociatedTokenAccount, CreateMint, MintTo,
    },
    solana_keypair::Keypair,
    solana_message::Message,
    solana_pubkey::Pubkey,
    solana_signer::Signer,
    solana_transaction::Transaction,
    spend_pass::{error::SpendPassError, Pass, PassTerms},
};

const START: i64 = 1_800_000_000;

struct World {
    svm: LiteSVM,
    owner: Keypair,
    agent: Keypair,
    /// Controls the destination: Algebra's payer.
    payer: Keypair,
    mint: Pubkey,
    owner_token: Pubkey,
    destination: Pubkey,
}

fn world() -> World {
    let mut svm = LiteSVM::new();
    let bytes = include_bytes!(concat!(env!("CARGO_TARGET_TMPDIR"), "/../deploy/spend_pass.so"));
    svm.add_program(spend_pass::id(), bytes).unwrap();
    let mut clock = svm.get_sysvar::<Clock>();
    clock.unix_timestamp = START;
    svm.set_sysvar(&clock);

    let (owner, agent, payer) = (Keypair::new(), Keypair::new(), Keypair::new());
    for k in [&owner, &agent, &payer] {
        svm.airdrop(&k.pubkey(), 10_000_000_000).unwrap();
    }
    let mint = CreateMint::new(&mut svm, &owner).decimals(6).authority(&owner.pubkey()).send().unwrap();
    let owner_token = CreateAssociatedTokenAccount::new(&mut svm, &owner, &mint).owner(&owner.pubkey()).send().unwrap();
    let destination = CreateAssociatedTokenAccount::new(&mut svm, &payer, &mint).owner(&payer.pubkey()).send().unwrap();
    MintTo::new(&mut svm, &owner, &mint, &owner_token, 1_000_000_000).send().unwrap();
    World { svm, owner, agent, payer, mint, owner_token, destination }
}

fn terms() -> PassTerms {
    PassTerms { per_call_cap: 50_000, total_budget: 200_000, window_secs: 0, window_cap: 0, expires_at: START + 3_600 }
}

fn pass_address(owner: &Pubkey, id: u64) -> Pubkey {
    Pubkey::find_program_address(&[Pass::SEED, owner.as_ref(), &id.to_le_bytes()], &spend_pass::id()).0
}

fn vault_address(pass: &Pubkey, mint: &Pubkey) -> Pubkey {
    associated_token::get_associated_token_address(pass, mint)
}

fn send(svm: &mut LiteSVM, signers: &[&Keypair], ix: Instruction) -> Result<(), String> {
    svm.expire_blockhash();
    let message = Message::new(&[ix], Some(&signers[0].pubkey()));
    let tx = Transaction::new(signers, message, svm.latest_blockhash());
    svm.send_transaction(tx).map(|_| ()).map_err(|e| format!("{:?}", e.err))
}

/// Anchor's custom error code for `e`, as the runtime reports it.
fn code(e: SpendPassError) -> String {
    let n: u32 = e.into();
    format!("Custom({n})")
}

fn expect_refused<T: std::fmt::Debug>(r: Result<T, String>, what: &str) {
    let err = r.expect_err("should have been refused");
    assert!(err.contains(what), "wanted {what}, got {err}");
}

fn create(w: &mut World, id: u64, t: PassTerms) -> Result<Pubkey, String> {
    let pass = pass_address(&w.owner.pubkey(), id);
    let ix = Instruction {
        program_id: spend_pass::id(),
        accounts: spend_pass::accounts::CreatePass {
            owner: w.owner.pubkey(),
            agent: w.agent.pubkey(),
            mint: w.mint,
            destination: w.destination,
            pass,
            vault: vault_address(&pass, &w.mint),
            associated_token_program: ASSOCIATED_TOKEN_PROGRAM_ID,
            token_program: TOKEN_PROGRAM_ID,
            system_program: SYSTEM_PROGRAM_ID,
        }
        .to_account_metas(None),
        data: spend_pass::instruction::CreatePass { id, terms: t }.data(),
    };
    let owner = w.owner.insecure_clone();
    send(&mut w.svm, &[&owner], ix).map(|_| pass)
}

fn deposit(w: &mut World, pass: Pubkey, amount: u64) -> Result<(), String> {
    let ix = Instruction {
        program_id: spend_pass::id(),
        accounts: spend_pass::accounts::Deposit {
            owner: w.owner.pubkey(),
            pass,
            mint: w.mint,
            owner_token: w.owner_token,
            vault: vault_address(&pass, &w.mint),
            token_program: TOKEN_PROGRAM_ID,
        }
        .to_account_metas(None),
        data: spend_pass::instruction::Deposit { amount }.data(),
    };
    let owner = w.owner.insecure_clone();
    send(&mut w.svm, &[&owner], ix)
}

fn pull_by(w: &mut World, signer: &Keypair, pass: Pubkey, destination: Pubkey, amount: u64, intent: u8) -> Result<(), String> {
    let ix = Instruction {
        program_id: spend_pass::id(),
        accounts: spend_pass::accounts::Pull {
            agent: signer.pubkey(),
            pass,
            mint: w.mint,
            vault: vault_address(&pass, &w.mint),
            destination,
            token_program: TOKEN_PROGRAM_ID,
        }
        .to_account_metas(None),
        data: spend_pass::instruction::Pull { amount, intent: [intent; 32] }.data(),
    };
    send(&mut w.svm, &[signer], ix)
}

fn pull(w: &mut World, pass: Pubkey, amount: u64) -> Result<(), String> {
    let agent = w.agent.insecure_clone();
    let dest = w.destination;
    pull_by(w, &agent, pass, dest, amount, 9)
}

fn refund_by(w: &mut World, signer: &Keypair, pass: Pubkey, amount: u64) -> Result<(), String> {
    let ix = Instruction {
        program_id: spend_pass::id(),
        accounts: spend_pass::accounts::Refund {
            authority: signer.pubkey(),
            pass,
            mint: w.mint,
            vault: vault_address(&pass, &w.mint),
            destination: w.destination,
            token_program: TOKEN_PROGRAM_ID,
        }
        .to_account_metas(None),
        data: spend_pass::instruction::Refund { amount, intent: [9; 32] }.data(),
    };
    send(&mut w.svm, &[signer], ix)
}

fn manage(w: &mut World, signer: &Keypair, pass: Pubkey, data: Vec<u8>) -> Result<(), String> {
    let ix = Instruction {
        program_id: spend_pass::id(),
        accounts: spend_pass::accounts::Manage { owner: signer.pubkey(), pass }.to_account_metas(None),
        data,
    };
    send(&mut w.svm, &[signer], ix)
}

fn balance(w: &World, account: &Pubkey) -> u64 {
    let a = w.svm.get_account(account).unwrap();
    spl_token::state::Account::unpack(&a.data).unwrap().amount
}

fn state(w: &World, pass: &Pubkey) -> Pass {
    let a = w.svm.get_account(pass).unwrap();
    Pass::try_deserialize(&mut a.data.as_ref()).unwrap()
}

fn set_time(w: &mut World, t: i64) {
    let mut clock = w.svm.get_sysvar::<Clock>();
    clock.unix_timestamp = t;
    w.svm.set_sysvar(&clock);
}

#[test]
fn a_pass_lives_its_whole_life() {
    let mut w = world();
    let pass = create(&mut w, 7, terms()).unwrap();
    let vault = vault_address(&pass, &w.mint);
    let p = state(&w, &pass);
    assert_eq!((p.owner, p.agent, p.destination, p.total_budget), (w.owner.pubkey(), w.agent.pubkey(), w.destination, 200_000));
    assert_eq!(w.svm.get_account(&pass).unwrap().data[0], 1, "account discriminator is one byte: 1");

    deposit(&mut w, pass, 300_000).unwrap();
    assert_eq!(balance(&w, &vault), 300_000);

    pull(&mut w, pass, 50_000).unwrap();
    assert_eq!((balance(&w, &vault), balance(&w, &w.destination.clone())), (250_000, 50_000));
    let p = state(&w, &pass);
    assert_eq!((p.spent, p.pulls, p.last_intent), (50_000, 1, [9; 32]));

    // The payment for that pull failed: the payer puts it back.
    let payer = w.payer.insecure_clone();
    refund_by(&mut w, &payer, pass, 20_000).unwrap();
    assert_eq!((balance(&w, &vault), state(&w, &pass).spent), (270_000, 30_000));

    // Kill switch.
    let owner = w.owner.insecure_clone();
    manage(&mut w, &owner, pass, spend_pass::instruction::SetFrozen { frozen: true }.data()).unwrap();
    expect_refused(pull(&mut w, pass, 1), &code(SpendPassError::PassFrozen));
    manage(&mut w, &owner, pass, spend_pass::instruction::SetFrozen { frozen: false }.data()).unwrap();
    pull(&mut w, pass, 1).unwrap();

    // Owner takes some back, then ends the pass.
    let ix = Instruction {
        program_id: spend_pass::id(),
        accounts: spend_pass::accounts::Withdraw {
            owner: owner.pubkey(),
            pass,
            mint: w.mint,
            owner_token: w.owner_token,
            vault,
            token_program: TOKEN_PROGRAM_ID,
        }
        .to_account_metas(None),
        data: spend_pass::instruction::Withdraw { amount: 100_000 }.data(),
    };
    send(&mut w.svm, &[&owner], ix).unwrap();
    assert_eq!(balance(&w, &vault), 169_999);

    manage(&mut w, &owner, pass, spend_pass::instruction::Revoke {}.data()).unwrap();
    expect_refused(pull(&mut w, pass, 1), &code(SpendPassError::PassRevoked));
    expect_refused(manage(&mut w, &owner, pass, spend_pass::instruction::Revoke {}.data()), &code(SpendPassError::AlreadyRevoked));

    let before = balance(&w, &w.owner_token.clone());
    let ix = Instruction {
        program_id: spend_pass::id(),
        accounts: spend_pass::accounts::ClosePass {
            owner: owner.pubkey(),
            pass,
            mint: w.mint,
            owner_token: w.owner_token,
            vault,
            token_program: TOKEN_PROGRAM_ID,
        }
        .to_account_metas(None),
        data: spend_pass::instruction::ClosePass {}.data(),
    };
    send(&mut w.svm, &[&owner], ix).unwrap();
    assert_eq!(balance(&w, &w.owner_token.clone()) - before, 169_999);
    assert!(w.svm.get_account(&pass).map_or(true, |a| a.lamports == 0));
    assert!(w.svm.get_account(&vault).map_or(true, |a| a.lamports == 0));
}

#[test]
fn every_limit_holds_on_chain() {
    let mut w = world();
    let pass = create(&mut w, 1, terms()).unwrap();
    deposit(&mut w, pass, 1_000_000).unwrap();

    expect_refused(pull(&mut w, pass, 0), &code(SpendPassError::ZeroAmount));
    expect_refused(pull(&mut w, pass, 50_001), &code(SpendPassError::OverPerCallCap));
    for _ in 0..4 {
        pull(&mut w, pass, 50_000).unwrap();
    }
    expect_refused(pull(&mut w, pass, 1), &code(SpendPassError::OverBudget));
    assert_eq!(state(&w, &pass).spent, 200_000);

    let pass2 = create(&mut w, 2, terms()).unwrap();
    deposit(&mut w, pass2, 100_000).unwrap();
    set_time(&mut w, START + 3_600);
    expect_refused(pull(&mut w, pass2, 1), &code(SpendPassError::PassExpired));
}

#[test]
fn a_window_cap_holds_on_chain() {
    let mut w = world();
    let t = PassTerms { window_secs: 60, window_cap: 80_000, ..terms() };
    let pass = create(&mut w, 3, t).unwrap();
    deposit(&mut w, pass, 1_000_000).unwrap();
    pull(&mut w, pass, 50_000).unwrap();
    expect_refused(pull(&mut w, pass, 30_001), &code(SpendPassError::OverWindowCap));
    pull(&mut w, pass, 30_000).unwrap();
    set_time(&mut w, START + 60);
    pull(&mut w, pass, 50_000).unwrap();
}

#[test]
fn only_the_agent_pulls_and_only_to_the_destination() {
    let mut w = world();
    let pass = create(&mut w, 1, terms()).unwrap();
    deposit(&mut w, pass, 100_000).unwrap();

    let stranger = Keypair::new();
    w.svm.airdrop(&stranger.pubkey(), 1_000_000_000).unwrap();
    let dest = w.destination;
    expect_refused(pull_by(&mut w, &stranger, pass, dest, 1, 0), "Custom(2001)"); // has_one = agent

    let owner = w.owner.insecure_clone();
    expect_refused(pull_by(&mut w, &owner, pass, dest, 1, 0), "Custom(2001)");

    // A token account of the agent's own: not the pass's destination.
    let mint = w.mint;
    let elsewhere = CreateAssociatedTokenAccount::new(&mut w.svm, &stranger, &mint).owner(&stranger.pubkey()).send().unwrap();
    let agent = w.agent.insecure_clone();
    expect_refused(pull_by(&mut w, &agent, pass, elsewhere, 1, 0), "Custom(2001)");
    assert_eq!(balance(&w, &vault_address(&pass, &w.mint)), 100_000);
}

#[test]
fn only_the_owner_manages() {
    let mut w = world();
    let pass = create(&mut w, 1, terms()).unwrap();
    let agent = w.agent.insecure_clone();
    expect_refused(manage(&mut w, &agent, pass, spend_pass::instruction::SetFrozen { frozen: true }.data()), "Custom(2001)");
    expect_refused(manage(&mut w, &agent, pass, spend_pass::instruction::SetAgent { agent: agent.pubkey() }.data()), "Custom(2001)");
    expect_refused(manage(&mut w, &agent, pass, spend_pass::instruction::Revoke {}.data()), "Custom(2001)");

    // Rotating the agent: the old key stops working at once.
    let owner = w.owner.insecure_clone();
    let next = Keypair::new();
    w.svm.airdrop(&next.pubkey(), 1_000_000_000).unwrap();
    deposit(&mut w, pass, 100_000).unwrap();
    manage(&mut w, &owner, pass, spend_pass::instruction::SetAgent { agent: next.pubkey() }.data()).unwrap();
    expect_refused(pull(&mut w, pass, 1), "Custom(2001)");
    let dest = w.destination;
    pull_by(&mut w, &next, pass, dest, 1, 0).unwrap();
    expect_refused(
        manage(&mut w, &owner, pass, spend_pass::instruction::SetAgent { agent: owner.pubkey() }.data()),
        &code(SpendPassError::InvalidAgent),
    );
}

#[test]
fn refunds_are_bounded_and_come_only_from_the_destination() {
    let mut w = world();
    let pass = create(&mut w, 1, terms()).unwrap();
    deposit(&mut w, pass, 100_000).unwrap();
    pull(&mut w, pass, 10_000).unwrap();

    let payer = w.payer.insecure_clone();
    expect_refused(refund_by(&mut w, &payer, pass, 10_001), &code(SpendPassError::RefundOverSpent));
    expect_refused(refund_by(&mut w, &payer, pass, 0), &code(SpendPassError::ZeroAmount));
    // The agent doesn't control the destination's tokens.
    let agent = w.agent.insecure_clone();
    expect_refused(refund_by(&mut w, &agent, pass, 1), "Custom(2015)"); // token::authority

    // Refunds work even on a frozen pass.
    let owner = w.owner.insecure_clone();
    manage(&mut w, &owner, pass, spend_pass::instruction::SetFrozen { frozen: true }.data()).unwrap();
    refund_by(&mut w, &payer, pass, 10_000).unwrap();
    assert_eq!(state(&w, &pass).spent, 0);
}

#[test]
fn bad_terms_and_bad_parties_are_refused_at_creation() {
    let mut w = world();
    expect_refused(create(&mut w, 1, PassTerms { per_call_cap: 0, ..terms() }), &code(SpendPassError::InvalidTerms));
    expect_refused(create(&mut w, 1, PassTerms { expires_at: START, ..terms() }), &code(SpendPassError::InvalidTerms));
    expect_refused(create(&mut w, 1, PassTerms { total_budget: 1, ..terms() }), &code(SpendPassError::InvalidTerms));

    // The owner can't name themselves as the agent.
    let owner = w.owner.insecure_clone();
    w.agent = owner.insecure_clone();
    expect_refused(create(&mut w, 1, terms()), &code(SpendPassError::InvalidAgent));
}

#[test]
fn a_pass_id_is_used_once() {
    let mut w = world();
    create(&mut w, 5, terms()).unwrap();
    assert!(create(&mut w, 5, terms()).is_err());
    create(&mut w, 6, terms()).unwrap();
}
