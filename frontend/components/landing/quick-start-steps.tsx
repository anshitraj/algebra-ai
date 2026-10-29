import { Logo } from "@/components/logo";
import { SolanaMark } from "@/components/chain-logos";
import { IconCheck, IconLock, IconReceipt, IconShield, IconWallet } from "@/components/icons";
import styles from "./quick-start-steps.module.css";

function StepArrow() {
  return <svg viewBox="0 0 52 36" fill="none" aria-hidden="true"><path d="M3 5C29-3 44 9 44 29m-7-7 7 8 6-9" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" /></svg>;
}

export function QuickStartSteps() {
  return (
    <section id="getting-started" aria-labelledby="quick-start-heading" className={`landing-container ${styles.section}`}>
      <div className="section-heading">
        <div><span className="eyebrow"><i /> A SIMPLE PLACE TO START</span><h2 id="quick-start-heading">Three steps.<br /><em>Your agent is ready.</em></h2></div>
        <p>Fund the work, set the boundaries, and let Algebra handle each request.</p>
      </div>
      <ol className={styles.steps}>
        <li className={styles.step}>
          <div className={styles.annotation}><span>Step 1</span><StepArrow /></div>
          <article className={styles.card}>
            <h3>Open your vault.</h3>
            <div className={`${styles.illustration} ${styles.funding}`} aria-hidden="true">
              <div className={styles.wallet}><IconWallet size={19} /><span>Your wallet</span><span className={styles.walletLine} /></div>
              <div className={styles.depositLine}><span />↓<small>SET A LIMIT</small></div>
              <div className={styles.vault}><div><SolanaMark size={23} /><span>Capped limit</span><IconLock size={15} /></div><strong>USDC</strong><small>Stays in your wallet. Bounded by your pass.</small><div className={styles.vaultDial}><span /><i /></div></div>
            </div>
            <p>Your vault is your own wallet, made in your browser: Algebra never holds your keys. Give a pass a capped USDC limit and the Solana program checks it before any funds can move. Testnet starts with free test credits.</p>
          </article>
        </li>
        <li className={styles.step}>
          <div className={styles.annotation}><span>Step 2</span><StepArrow /></div>
          <article className={styles.card}>
            <h3>Set your agent&rsquo;s limits.</h3>
            <div className={`${styles.illustration} ${styles.authority}`} aria-hidden="true">
              <div className={styles.agentLogos}>{["claude", "openai", "cursor"].map((name) => <span key={name}>
                {/* These bundled brand marks are local illustration assets. */}
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img src={`/connectors/${name}.svg`} width="22" height="22" alt="" />
              </span>)}</div>
              <div className={styles.pass}><div><Logo size={20} /><strong>Spend Pass</strong><IconShield size={16} /></div><p><span>Total budget</span><strong>10 USDC</strong></p><p><span>Per-call limit</span><strong>0.01 USDC</strong></p><p><span>Provider rules</span><strong>Set by you</strong></p></div>
              <div className={styles.passBadge}><IconLock size={14} /> Permission to spend</div>
            </div>
            <p>Give your agent a Spend Pass with a budget, per-call limit, and provider rules. You keep control of the authority.</p>
          </article>
        </li>
        <li className={styles.step}>
          <div className={styles.annotation}><span>Step 3</span><StepArrow /></div>
          <article className={styles.card}>
            <h3>Get the result. Keep the proof.</h3>
            <div className={`${styles.illustration} ${styles.proof}`} aria-hidden="true">
              <div className={styles.receiptOrbit} /><div className={styles.receiptBack} /><div className={styles.receiptBackTwo} />
              <div className={styles.receipt}><IconReceipt size={23} /><strong>Execution receipt</strong><p><span>Policy checked</span><IconCheck size={12} /></p><p><span>USDC settled</span><IconCheck size={12} /></p><p><span>Result delivered</span><IconCheck size={12} /></p><small>Signed &amp; verifiable</small></div>
              <span className={styles.verified}><IconCheck size={24} /></span>
            </div>
            <p>Algebra finds an eligible API, pays within your rules, and returns the result with a signed receipt you can verify.</p>
          </article>
        </li>
      </ol>
      <p className={styles.note}>Illustrative setup · amounts shown are examples · on-chain limits run on Solana devnet today; Mainnet opens after an independent security review.</p>
    </section>
  );
}
