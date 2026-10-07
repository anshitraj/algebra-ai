import type { Metadata } from "next";
import { ContactLines, LegalDoc, Section } from "@/components/legal-doc";
import { LEGAL } from "@/lib/legal";

export const metadata: Metadata = {
  title: "Terms of Service — Algebra",
  description: "The agreement between you and Algebra: your account, Spend Passes and approvals, providers, payments on Solana, and what Algebra does not promise.",
};

export default function TermsPage() {
  return (
    <LegalDoc
      title="Terms of Service"
      intro={`These terms are the agreement between you and ${LEGAL.who} for using Algebra's website, console, API and MCP server. By creating an account or using the service, you accept them.`}
    >
      <Section title="What Algebra is">
        <p>
          Algebra is a router and a spend firewall for AI agents that pay for APIs. An agent says what it wants done and the most it will pay. Algebra
          finds providers of that work, asks each for its price, checks the request against the limits you set, pays the provider in USDC on Solana
          from a wallet Algebra controls, makes the call, checks the result and gives back the result with a signed receipt.
        </p>
        <p>
          Algebra is software. It is not a bank, an exchange, a money transmitter or an investment adviser, and it does not hold balances for you. A
          Spend Pass is a limit on what an agent may spend, not a deposit. You never give Algebra, or your agent, a card number, a wallet private key
          or any other secret of yours.
        </p>
      </Section>

      <Section title="Your account">
        <ul>
          <li>You must be at least 18 and able to enter a binding contract.</li>
          <li>Give accurate details and keep your sign-in secure. You are responsible for what happens under your account.</li>
          <li>Tell us promptly if you think someone else has accessed it. You can sign any device out under Account.</li>
        </ul>
      </Section>

      <Section title="Spend Passes, limits and approvals">
        <ul>
          <li>
            You decide the rules for each agent: a budget, the most one call may cost, the amount above which a call waits for your approval, which
            providers it may pay, how fast it may spend, and what to do about a provider you have never paid. Algebra checks every payment against
            them on our servers before it is made.
          </li>
          <li>
            A call inside the limits you set can be paid without asking you again. A payment inside your limits, or one you approve, is your payment.
            Approvals are yours to give: an agent cannot approve its own spending.
          </li>
          <li>
            The kill switch freezes every pass at once, including a payment about to be made. A payment already confirmed on the blockchain cannot be
            stopped.
          </li>
          <li>
            Agents use AI models and can make mistakes: they can ask for the wrong thing, or for too much of it. Set limits you can live with. A dry
            run (&quot;simulate&quot;) shows what a request would do without paying anything.
          </li>
        </ul>
      </Section>

      <Section title="Providers and what you buy">
        <p>
          The APIs your agents pay for are run by other people. Algebra reads public catalogs of them (Pay.sh, Circle&apos;s Agent Marketplace,
          PayAI and Coinbase&apos;s x402 Bazaar) and, when asked and configured, searches the open web. A listing is not an endorsement, and a listed
          price is not a quote: Algebra asks the provider for its real price before paying, and refuses providers that are down, that ask more than they
          list or that ask many times what the same work usually costs.
        </p>
        <p>
          Your contract for the work is with the provider, under its own terms. What a provider returns is third-party data: Algebra checks that it
          arrived and that it has the shape expected, which is not a promise that it is correct, complete or fit for your purpose.
        </p>
      </Section>

      <Section title="Payments on Solana, and swaps">
        <ul>
          <li>
            Payments are in USDC on Solana. A confirmed blockchain payment is final: Algebra cannot reverse it, and refunds, if any, are for the
            provider to give.
          </li>
          <li>
            Payments are public on the blockchain: the wallet addresses, the amount and the time can be seen by anyone, including who was paid.
          </li>
          <li>
            Where Algebra offers it, an agent can buy a token with USDC through Jupiter. Token prices move, a swap can lose value, and Algebra gives no
            advice about whether to make one. Algebra signs a swap only if the transaction would spend no more than the amount authorized and would
            deliver at least the quote less the slippage you allowed; that limits what one swap can cost, not what the token is worth afterwards.
          </li>
          <li>
            Networks marked devnet or sandbox use test money with no value. Do not send real funds to a test address.
          </li>
        </ul>
      </Section>

      <Section title="Receipts">
        <p>
          Every payment comes with a receipt that Algebra signs. It records what was asked, which provider did it, what was paid and what the chain
          showed, and it can be checked by anyone against Algebra&apos;s published keys. It proves what Algebra recorded; it does not prove that the
          provider&apos;s answer is true.
        </p>
      </Section>

      <Section title="Fair use">
        <p>Don&apos;t use Algebra to:</p>
        <ul>
          <li>pay for anything illegal, or anything a provider&apos;s terms forbid;</li>
          <li>get around your own limits, ours or a provider&apos;s, for example by splitting one payment into several;</li>
          <li>disrupt, overload, scrape or reverse-engineer the service, or access accounts or data that aren&apos;t yours;</li>
          <li>create accounts in bulk, or resell access without our written agreement.</li>
        </ul>
        <p>Rate limits keep the service fair for everyone, and web searches for providers are limited to a few an hour for each agent.</p>
      </Section>

      <Section title="Services we rely on">
        <p>
          Algebra depends on services we do not control: the Solana network and the node providers that serve it, Circle (USDC), the provider
          catalogs named above, Jupiter for swaps, AI model providers such as Google Gemini, Anthropic and OpenAI for the console&apos;s agent and for
          web search, and an email provider for account emails. Their availability and terms are outside our control.
        </p>
      </Section>

      <Section title="Disclaimers and liability">
        <p>
          This is early software. We work hard to keep it correct and available, but the service is provided &quot;as is&quot;. To the extent the law
          allows, we are not liable for indirect or consequential loss, for a provider&apos;s acts or answers, for losses from the price of a token
          or from a payment the blockchain has confirmed, or for what a third-party catalog says. Our total liability to you for any claim is limited
          to what you paid us in the three months before it arose, and nothing here limits a right you have under a law that cannot be limited by
          contract.
        </p>
      </Section>

      <Section title="Suspension and ending">
        <p>
          You can stop using Algebra and delete your account at any time under Account. We may suspend or close an account that breaks these terms or
          puts others at risk, and will tell you why unless the law or safety prevents it.
        </p>
      </Section>

      <Section title="Changes">
        <p>
          We may update these terms. For material changes we will give notice in the app or by email before they apply. Continuing to use Algebra
          after that means you accept the new terms.
        </p>
      </Section>

      <Section title="Law and disputes">
        <p>
          These terms are governed by the laws of India.{" "}
          {LEGAL.jurisdiction
            ? `Courts in ${LEGAL.jurisdiction} have jurisdiction over any dispute.`
            : "Courts in India with competent jurisdiction hear any dispute."}{" "}
          Please contact us first — most problems are solved faster that way.
        </p>
      </Section>

      <Section title="Contact">
        <ContactLines />
      </Section>
    </LegalDoc>
  );
}
