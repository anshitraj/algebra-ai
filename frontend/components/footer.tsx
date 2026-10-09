import Link from "next/link";
import { Logo } from "./logo";
import { IconArrowUp, IconExternal } from "./icons";

const docs = "https://github.com/anshitraj/algebra-ai/blob/main/docs/MCP.md";

export function Footer() {
  return (
    <footer className="landing-footer">
      <div className="landing-container">
        <div className="footer-top">
          <Link href="/" className="wordmark"><Logo size={29} /><span>algebra</span></Link>
          <p>The execution layer<br />for the agent economy.</p>
          <div>
            <span>PRODUCT</span>
            <Link href="/#product">The workspace</Link>
            <Link href="/#security">Spend firewall</Link>
            <Link href="/verify">Verify a receipt</Link>
          </div>
          <div>
            <span>BUILD</span>
            <a href={docs} target="_blank" rel="noopener noreferrer">Documentation <IconExternal size={11} /></a>
            <a href="https://github.com/anshitraj/algebra-ai" target="_blank" rel="noopener noreferrer">GitHub <IconExternal size={11} /></a>
            <Link href="/demo">Interactive demo</Link>
          </div>
          <div>
            <span>COMPANY</span>
            <Link href="/contact">Contact</Link>
            <Link href="/privacy">Privacy</Link>
            <Link href="/terms">Terms</Link>
          </div>
        </div>
        <div className="footer-wordart" aria-hidden="true">algebra<span>↗</span></div>
        <div className="footer-bottom">
          <span>© {new Date().getFullYear()} Algebra · Apache-2.0</span>
          <span><i /> Built on Solana. Paid in USDC.</span>
          <Link href="/#main-content">Back to top <IconArrowUp size={13} /></Link>
        </div>
      </div>
    </footer>
  );
}
