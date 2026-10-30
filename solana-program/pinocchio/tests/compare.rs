//! The Anchor program and the Pinocchio program, side by side: the same
//! transactions must leave the same pass and the same balances, and the
//! Pinocchio program must refuse what Anchor's account constraints refused.
//! Prints each program's size and compute units.

use {
    anchor_lang::{
        prelude::Clock, solana_program::instruction::Instruction, solana_program::program_pack::Pack,
        system_program::ID as SYSTEM_PROGRAM_ID, AccountDeserialize, InstructionData, ToAccountMetas,
    },
    anchor_spl::{
        associated_token::{self, ID as ASSOCIATED_TOKEN_PROGRAM_ID},
        token::spl_token,
    },
    litesvm::LiteSVM,
    litesvm_token::{spl_token::ID as TOKEN_PROGRAM_ID, CreateAccount, CreateAssociatedTokenAccount, CreateMint, MintTo},
    solana_keypair::Keypair,
    solana_message::Message,
    solana_pubkey::Pubkey,
    solana_signer::Signer,
    solana_transaction::Transaction,
    spend_pass::{Pass, PassTerms},
};

const START: i64 = 1_800_000_000;
const PINOCCHIO: Pubkey = Pubkey::from_str_const("F1Uu8ynQUviLZHUgejHDMkKmFAJJ7ZKDwU3Svc7Xpj21");
const ANCHOR_SO: &[u8] = include_bytes!(concat!(env!("CARGO_MANIFEST_DIR"), "/../target/deploy/spend_pass.so"));
const PINOCCHIO_SO: &[u8] = include_bytes!(concat!(env!("CARGO_TARGET_TMPDIR"), "/../deploy/spend_pass_pinocchio.so"));

struct World {
    svm: LiteSVM,
    program: Pubkey,
    owner: Keypair,
    agent: Keypair,
    payer: Keypair,
    mint: Pubkey,
    owner_token: Pubkey,
    destination: Pubkey,
}

fn world(program: Pubkey, so: &[u8]) -> World {
    let mut svm = LiteSVM::new();
    svm.add_program(program, so).unwrap();
    let mut clock = svm.get_sysvar::<Clock>();
    clock.unix_timestamp = START;
    svm.set_sysvar(&clock);
    // Fixed keys, so both worlds have the same parties and mint.
    let owner = Keypair::new_from_array([1; 32]);
    let agent = Keypair::new_from_array([2; 32]);
    let payer = Keypair::new_from_array([3; 32]);
    let mint_kp = Keypair::new_from_array([4; 32]);
    for k in [&owner, &agent, &payer] {
        svm.airdrop(&k.pubkey(), 10_000_000_000).unwrap();
    }
    let mint = CreateMint::new(&mut svm, &owner).decimals(6).authority(&owner.pubkey()).token_program_id(&TOKEN_PROGRAM_ID).send().unwrap();
    let _ = mint_kp;
    let owner_token = CreateAssociatedTokenAccount::new(&mut svm, &owner, &mint).owner(&owner.pubkey()).send().unwrap();
    let destination = CreateAssociatedTokenAccount::new(&mut svm, &payer, &mint).owner(&payer.pubkey()).send().unwrap();
    MintTo::new(&mut svm, &owner, &mint, &owner_token, 1_000_000_000).send().unwrap();
    World { svm, program, owner, agent, payer, mint, owner_token, destination }
}

/// Builds the instruction first, then borrows the world to send it.
macro_rules! tx {
    ($w:expr, $signers:expr, $ix:expr) => {{
        let ix = $ix;
        send_tx($w, $signers, ix)
    }};
}

fn send_tx(w: &mut World, signers: &[&Keypair], ix: Instruction) -> Result<u64, String> {
    w.svm.expire_blockhash();
    let msg = Message::new(&[ix], Some(&signers[0].pubkey()));
    let tx = Transaction::new(signers, msg, w.svm.latest_blockhash());
    w.svm.send_transaction(tx).map(|m| m.compute_units_consumed).map_err(|e| format!("{:?} {:?}", e.err, e.meta.logs))
}

fn pass_of(w: &World, id: u64) -> Pubkey {
    Pubkey::find_program_address(&[b"pass", w.owner.pubkey().as_ref(), &id.to_le_bytes()], &w.program).0
}

fn create_ix(w: &World, id: u64, terms: PassTerms) -> Instruction {
    let pass = pass_of(w, id);
    Instruction {
        program_id: w.program,
        accounts: spend_pass::accounts::CreatePass {
            owner: w.owner.pubkey(),
            agent: w.agent.pubkey(),
            mint: w.mint,
            destination: w.destination,
            pass,
            vault: associated_token::get_associated_token_address(&pass, &w.mint),
            associated_token_program: ASSOCIATED_TOKEN_PROGRAM_ID,
            token_program: TOKEN_PROGRAM_ID,
            system_program: SYSTEM_PROGRAM_ID,
        }
        .to_account_metas(None),
        data: spend_pass::instruction::CreatePass { id, terms }.data(),
    }
}

fn deposit_ix(w: &World, pass: Pubkey, amount: u64) -> Instruction {
    Instruction {
        program_id: w.program,
        accounts: spend_pass::accounts::Deposit {
            owner: w.owner.pubkey(),
            pass,
            mint: w.mint,
            owner_token: w.owner_token,
            vault: associated_token::get_associated_token_address(&pass, &w.mint),
            token_program: TOKEN_PROGRAM_ID,
        }
        .to_account_metas(None),
        data: spend_pass::instruction::Deposit { amount }.data(),
    }
}

fn pull_ix(w: &World, pass: Pubkey, vault: Pubkey, amount: u64) -> Instruction {
    Instruction {
        program_id: w.program,
        accounts: spend_pass::accounts::Pull { agent: w.agent.pubkey(), pass, mint: w.mint, vault, destination: w.destination, token_program: TOKEN_PROGRAM_ID }
            .to_account_metas(None),
        data: spend_pass::instruction::Pull { amount, intent: [7; 32] }.data(),
    }
}

fn refund_ix(w: &World, pass: Pubkey, amount: u64) -> Instruction {
    Instruction {
        program_id: w.program,
        accounts: spend_pass::accounts::Refund {
            authority: w.payer.pubkey(),
            pass,
            mint: w.mint,
            vault: associated_token::get_associated_token_address(&pass, &w.mint),
            destination: w.destination,
            token_program: TOKEN_PROGRAM_ID,
        }
        .to_account_metas(None),
        data: spend_pass::instruction::Refund { amount, intent: [8; 32] }.data(),
    }
}

fn manage_ix(w: &World, pass: Pubkey, data: Vec<u8>) -> Instruction {
    Instruction { program_id: w.program, accounts: spend_pass::accounts::Manage { owner: w.owner.pubkey(), pass }.to_account_metas(None), data }
}

fn close_ix(w: &World, pass: Pubkey) -> Instruction {
    Instruction {
        program_id: w.program,
        accounts: spend_pass::accounts::ClosePass {
            owner: w.owner.pubkey(),
            pass,
            mint: w.mint,
            owner_token: w.owner_token,
            vault: associated_token::get_associated_token_address(&pass, &w.mint),
            token_program: TOKEN_PROGRAM_ID,
        }
        .to_account_metas(None),
        data: spend_pass::instruction::ClosePass {}.data(),
    }
}

fn balance(w: &World, a: &Pubkey) -> u64 {
    spl_token::state::Account::unpack(&w.svm.get_account(a).unwrap().data).unwrap().amount
}

fn state(w: &World, pass: &Pubkey) -> Pass {
    Pass::try_deserialize(&mut w.svm.get_account(pass).unwrap().data.as_ref()).unwrap()
}

fn terms() -> PassTerms {
    PassTerms { per_call_cap: 50_000, total_budget: 200_000, window_secs: 3_600, window_cap: 120_000, expires_at: START + 86_400 }
}

/// Runs one life of a pass and returns what it left, plus the compute each step took.
fn life(w: &mut World) -> (Vec<u64>, Vec<(&'static str, u64)>) {
    let owner = w.owner.insecure_clone();
    let agent = w.agent.insecure_clone();
    let payer = w.payer.insecure_clone();
    let pass = pass_of(w, 9);
    let vault = associated_token::get_associated_token_address(&pass, &w.mint);
    let mut cu = vec![];
    cu.push(("create_pass", tx!(w, &[&owner], create_ix(w, 9, terms())).unwrap()));
    cu.push(("deposit", tx!(w, &[&owner], deposit_ix(w, pass, 300_000)).unwrap()));
    cu.push(("pull", tx!(w, &[&agent], pull_ix(w, pass, vault, 50_000)).unwrap()));
    tx!(w, &[&agent], pull_ix(w, pass, vault, 50_000)).unwrap();
    assert!(tx!(w, &[&agent], pull_ix(w, pass, vault, 50_000)).unwrap_err().contains("Custom(6006)"), "window cap"); // OverWindowCap
    cu.push(("refund", tx!(w, &[&payer], refund_ix(w, pass, 30_000)).unwrap()));
    cu.push(("set_frozen", tx!(w, &[&owner], manage_ix(w, pass, spend_pass::instruction::SetFrozen { frozen: true }.data())).unwrap()));
    assert!(tx!(w, &[&agent], pull_ix(w, pass, vault, 1)).unwrap_err().contains("Custom(6001)"), "frozen"); // PassFrozen
    tx!(w, &[&owner], manage_ix(w, pass, spend_pass::instruction::SetFrozen { frozen: false }.data())).unwrap();
    let s = state(w, &pass);
    let out = vec![s.spent, s.pulls, s.window_spent, s.window_start as u64, s.total_budget, balance(w, &vault), balance(w, &w.destination), s.frozen as u64];
    let before = balance(w, &w.owner_token);
    cu.push(("close_pass", tx!(w, &[&owner], close_ix(w, pass)).unwrap()));
    assert_eq!(balance(w, &w.owner_token) - before, out[5], "close returns the vault");
    assert!(w.svm.get_account(&pass).map_or(true, |a| a.lamports == 0));
    (out, cu)
}

#[test]
fn both_programs_leave_the_same_state_and_balances() {
    let mut a = world(spend_pass::id(), ANCHOR_SO);
    let mut p = world(PINOCCHIO, PINOCCHIO_SO);
    let (sa, cua) = life(&mut a);
    let (sp, cup) = life(&mut p);
    assert_eq!(sa, sp, "spent, pulls, window_spent, window_start, budget, vault, destination, frozen");
    println!("program size: anchor {} bytes, pinocchio {} bytes", ANCHOR_SO.len(), PINOCCHIO_SO.len());
    for ((name, x), (_, y)) in cua.iter().zip(cup.iter()) {
        println!("{name:<12} anchor {x:>6} CU   pinocchio {y:>6} CU");
    }
}

#[test]
fn a_fake_pass_from_another_program_is_refused() {
    let mut w = world(PINOCCHIO, PINOCCHIO_SO);
    let owner = w.owner.insecure_clone();
    let agent = w.agent.insecure_clone();
    let pass = pass_of(&w, 1);
    tx!(&mut w, &[&owner], create_ix(&w, 1, terms())).unwrap();
    tx!(&mut w, &[&owner], deposit_ix(&w, pass, 100_000)).unwrap();
    // A copy of a real pass's bytes, with a huge cap, in an account this
    // program doesn't own: an attacker's "pass".
    let mut fake = w.svm.get_account(&pass).unwrap();
    let mut data = fake.data.clone();
    data[137..145].copy_from_slice(&u64::MAX.to_le_bytes());
    fake.data = data;
    fake.owner = Pubkey::new_unique();
    let fake_key = Pubkey::new_unique();
    w.svm.set_account(fake_key, fake).unwrap();
    let vault = associated_token::get_associated_token_address(&pass, &w.mint);
    let err = tx!(&mut w, &[&agent], pull_ix(&w, fake_key, vault, 60_000)).unwrap_err();
    assert!(err.contains("IllegalOwner"), "{err}");
}

#[test]
fn a_pass_at_the_wrong_address_is_refused() {
    let mut w = world(PINOCCHIO, PINOCCHIO_SO);
    let owner = w.owner.insecure_clone();
    let agent = w.agent.insecure_clone();
    let pass = pass_of(&w, 1);
    tx!(&mut w, &[&owner], create_ix(&w, 1, terms())).unwrap();
    // The same bytes, owned by this program, at an address its seeds don't give.
    let mut copy = w.svm.get_account(&pass).unwrap();
    copy.data[137..145].copy_from_slice(&u64::MAX.to_le_bytes());
    let other = Pubkey::new_unique();
    w.svm.set_account(other, copy).unwrap();
    let vault = associated_token::get_associated_token_address(&pass, &w.mint);
    let err = tx!(&mut w, &[&agent], pull_ix(&w, other, vault, 1)).unwrap_err();
    assert!(err.contains("InvalidSeeds"), "{err}");
}

#[test]
fn only_the_pass_vault_can_be_drawn_from() {
    let mut w = world(PINOCCHIO, PINOCCHIO_SO);
    let owner = w.owner.insecure_clone();
    let agent = w.agent.insecure_clone();
    let pass = pass_of(&w, 1);
    tx!(&mut w, &[&owner], create_ix(&w, 1, terms())).unwrap();
    tx!(&mut w, &[&owner], deposit_ix(&w, pass, 100_000)).unwrap();
    // A second token account the pass owns, but not its associated one.
    let mint = w.mint;
    let other = CreateAccount::new(&mut w.svm, &owner, &mint).owner(&pass).send().unwrap();
    let err = tx!(&mut w, &[&agent], pull_ix(&w, pass, other, 1)).unwrap_err();
    assert!(err.contains("InvalidSeeds"), "{err}");
}

#[test]
fn a_wrong_token_program_is_refused() {
    let mut w = world(PINOCCHIO, PINOCCHIO_SO);
    let owner = w.owner.insecure_clone();
    let agent = w.agent.insecure_clone();
    let pass = pass_of(&w, 1);
    tx!(&mut w, &[&owner], create_ix(&w, 1, terms())).unwrap();
    tx!(&mut w, &[&owner], deposit_ix(&w, pass, 100_000)).unwrap();
    let vault = associated_token::get_associated_token_address(&pass, &w.mint);
    let mut ix = pull_ix(&w, pass, vault, 1);
    ix.accounts[5].pubkey = SYSTEM_PROGRAM_ID;
    let err = tx!(&mut w, &[&agent], ix).unwrap_err();
    assert!(err.contains("IncorrectProgramId"), "{err}");
}

#[test]
fn a_pass_address_someone_prefunded_can_still_be_made() {
    let mut w = world(PINOCCHIO, PINOCCHIO_SO);
    let owner = w.owner.insecure_clone();
    let pass = pass_of(&w, 3);
    // A griefer sends lamports to the pass's address before it exists.
    w.svm.airdrop(&pass, 1_000).unwrap();
    tx!(&mut w, &[&owner], create_ix(&w, 3, terms())).unwrap();
    assert_eq!(state(&w, &pass).owner, owner.pubkey());
    assert_eq!(w.svm.get_account(&pass).unwrap().owner, PINOCCHIO);
}

#[test]
fn an_unsigned_or_short_instruction_is_refused() {
    let mut w = world(PINOCCHIO, PINOCCHIO_SO);
    let owner = w.owner.insecure_clone();
    let agent = w.agent.insecure_clone();
    let pass = pass_of(&w, 1);
    tx!(&mut w, &[&owner], create_ix(&w, 1, terms())).unwrap();
    let vault = associated_token::get_associated_token_address(&pass, &w.mint);
    let mut ix = pull_ix(&w, pass, vault, 1);
    ix.data.truncate(20);
    assert!(tx!(&mut w, &[&agent], ix).unwrap_err().contains("InvalidInstructionData"));
    let mut ix = pull_ix(&w, pass, vault, 1);
    ix.data[0] = 99;
    assert!(tx!(&mut w, &[&agent], ix).unwrap_err().contains("InvalidInstructionData"));
    // The agent's signature is required: present the agent as not signing.
    let mut ix = pull_ix(&w, pass, vault, 1);
    ix.accounts[0].is_signer = false;
    assert!(tx!(&mut w, &[&owner], ix).unwrap_err().contains("MissingRequiredSignature"));
    // A frozen flag that isn't 0 or 1.
    assert!(tx!(&mut w, &[&owner], manage_ix(&w, pass, vec![3, 2])).unwrap_err().contains("InvalidInstructionData"));
}
