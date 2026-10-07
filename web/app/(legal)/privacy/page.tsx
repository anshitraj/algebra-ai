import type { Metadata } from "next";
import { ContactLines, LegalDoc, Section } from "@/components/legal-doc";
import { LEGAL } from "@/lib/legal";

export const metadata: Metadata = {
  title: "Privacy Policy — Algebra",
  description: "What Algebra collects, what leaves it when an agent makes a request, who it goes to, how long it is kept, and how to see, correct or delete it.",
};

export default function PrivacyPage() {
  return (
    <LegalDoc
      title="Privacy Policy"
      intro={`This policy explains what personal data ${LEGAL.who} collects when you use Algebra, what leaves Algebra when your agent makes a request, who it goes to, and the rights you have over it under India's Digital Personal Data Protection Act, 2023. We collect only what the service needs, and we never sell your data.`}
    >
      <Section title="What we collect">
        <ul>
          <li>
            <strong>Account</strong> — your name, email address, and either a password (stored only as an argon2id hash) or the ID of the Google or
            GitHub account you sign in with.
          </li>
          <li>
            <strong>Spend Passes and agent tokens</strong> — each pass&apos;s label, limits and controls. A token is shown to you once and stored only
            as a hash.
          </li>
          <li>
            <strong>Requests your agents make</strong> — what each asked for (the kind of work, its input, the most it would pay), which providers
            were priced and chosen, the decisions made, what was paid, and the signed receipt. The input is whatever your agent sent, so it can contain
            personal data if your agent put it there: send only what the work needs.
          </li>
          <li>
            <strong>Answers</strong> — the provider&apos;s answer to a paid request is kept so that asking again returns it instead of paying twice.
            It is encrypted at rest and deleted after 24 hours unless the operator of the instance sets another time (never more than 7 days), and a
            request can ask for its answer not to be kept at all. Otherwise we keep only a hash of it.
          </li>
          <li>
            <strong>Agent chats</strong> — your messages and the agent&apos;s replies are sent to our server and to the AI provider to produce each
            reply. The conversation itself is kept in your browser, not in our database; clearing it or starting a new chat removes it.
          </li>
          <li>
            <strong>Devices</strong> — for each signed-in session, its IP address, browser and last activity, so you can see and sign out devices.
          </li>
        </ul>
        <p>We never collect card numbers, wallet private keys or one-time passwords.</p>
      </Section>

      <Section title="What leaves Algebra when your agent makes a request">
        <ul>
          <li>
            <strong>Price requests.</strong> To compare providers, Algebra asks each candidate (up to twelve) what it would charge, with a free request
            that carries your agent&apos;s input. Providers you did not choose therefore see what your agent asked for. If an input is sensitive, name
            the provider you want and the others are not asked.
          </li>
          <li>
            <strong>The provider that is paid</strong> receives the input again with the payment, and the payment on the blockchain shows the wallet,
            the amount and the time.
          </li>
          <li>
            <strong>Searching the web for providers</strong> (when your agent asks and the operator has set it up) sends a description of the kind of
            work wanted to Google&apos;s Gemini, not your agent&apos;s input. Endpoints it finds are priced with a sample input, never yours.
          </li>
          <li>
            <strong>Swaps</strong> send the token and amount to Jupiter and a transaction to a Solana node.
          </li>
        </ul>
      </Section>

      <Section title="Why we use it">
        <ul>
          <li>to run your account and keep it secure;</li>
          <li>to find providers, apply your limits, ask for your approval, pay, and prove what happened;</li>
          <li>to send account emails such as password resets;</li>
          <li>to prevent abuse and fraud, and to meet legal and accounting obligations.</li>
        </ul>
        <p>We process your data on the basis of your consent, given when you create an account, and for the legitimate uses the law allows.</p>
      </Section>

      <Section title="Who we share it with">
        <ul>
          <li>
            <strong>Providers</strong> — as described above: those asked for a price, and the one paid.
          </li>
          <li>
            <strong>The Solana network and its node providers</strong> — payments are public by nature.
          </li>
          <li>
            <strong>AI model providers</strong> (Google Gemini, Anthropic or OpenAI, depending on the model in use) — your chat messages and the
            results of the agent&apos;s searches, to generate replies.
          </li>
          <li>
            <strong>Our email provider and hosting providers</strong> — to send account emails and run the service.
          </li>
          <li>Authorities, when the law requires it.</li>
        </ul>
        <p>Some of these providers process data outside India, under their own security and privacy commitments.</p>
      </Section>

      <Section title="How long we keep it">
        <p>
          Account data stays while your account exists. When you delete your account we erase your name, email, sign-ins and sessions, revoke every
          agent and Spend Pass, delete every answer we kept and remove the input of your requests. The requests themselves, their receipts and the
          hash of each request are records the law and the blockchain require us to keep; after deletion they remain tied only to an anonymous ID, not
          to you. Sessions that have ended are cleared after a week.
        </p>
      </Section>

      <Section title="How we protect it">
        <p>
          Answers are encrypted at rest under a key used for nothing else. Sessions use secure, HTTP-only cookies. Passwords and agent tokens are
          hashed, never stored. Every payment is checked against your limits on our servers, outgoing requests can only reach public internet
          addresses, and access to data is limited to what each part of the service needs.
        </p>
      </Section>

      <Section title="Your rights">
        <ul>
          <li>
            <strong>See your data</strong> — download it anytime under Account → Your data: your account, Spend Passes, requests and devices. The
            answers we keep for repeat requests are readable through the API for as long as they are kept.
          </li>
          <li>
            <strong>Correct it</strong> — edit your name and limits in the console, or ask us.
          </li>
          <li>
            <strong>Erase it</strong> — delete your account under Account → Your data.
          </li>
          <li>
            <strong>Withdraw consent</strong> — by deleting your account; this doesn&apos;t affect processing that already happened.
          </li>
          <li>
            <strong>Nominate</strong> someone to exercise these rights for you, and <strong>raise a grievance</strong> with our Grievance Officer. If
            we don&apos;t resolve it, you can complain to the Data Protection Board of India.
          </li>
        </ul>
      </Section>

      <Section title="Cookies and browser storage">
        <p>
          We use one essential cookie to keep you signed in, and during Google or GitHub sign-in a short-lived one to protect that step. Your browser
          stores your current agent chat and chosen AI model. We use no advertising or tracking cookies and no third-party analytics.
        </p>
      </Section>

      <Section title="Children">
        <p>Algebra is not for anyone under 18, and we do not knowingly collect their data.</p>
      </Section>

      <Section title="Changes">
        <p>If we change this policy in a way that matters, we will tell you in the app or by email before the change applies.</p>
      </Section>

      <Section title="Contact and grievances">
        <ContactLines />
      </Section>
    </LegalDoc>
  );
}
