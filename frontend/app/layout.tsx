import type { Metadata } from "next";
import { Manrope, IBM_Plex_Mono, Instrument_Serif } from "next/font/google";
import "./globals.css";
import { MotionProvider } from "@/components/motion-provider";

const manrope = Manrope({
  variable: "--font-manrope",
  subsets: ["latin"],
  display: "swap",
});

const ibmMono = IBM_Plex_Mono({
  variable: "--font-ibm-mono",
  subsets: ["latin"],
  display: "swap",
  weight: ["400", "500"],
});

const editorial = Instrument_Serif({ variable: "--font-editorial", subsets: ["latin"], weight: "400", style: ["normal", "italic"], display: "swap" });

const description =
  "The router and spend firewall for AI agents that pay for APIs on Solana. An agent asks for an outcome; Algebra routes to the best paid API across Pay.sh, Circle's Agent Marketplace, PayAI and Coinbase's Bazaar, checks the Spend Pass, pays in USDC over x402, verifies the result and signs a receipt, without ever handing the agent a key.";

export const metadata: Metadata = {
  metadataBase: new URL(process.env.PUBLIC_WEB_URL || "http://localhost:3000"),
  title: "Algebra — permission to spend, not access to money",
  description,
  openGraph: { title: "Algebra — permission to spend, not access to money", description, siteName: "Algebra", type: "website", locale: "en_US" },
  twitter: { card: "summary", title: "Algebra", description },
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html
      lang="en"
      data-scroll-behavior="smooth"
      className={`${manrope.variable} ${ibmMono.variable} ${editorial.variable}`}
    >
      <body className="min-h-screen antialiased selection:bg-primary selection:text-primary-tint">
        <MotionProvider>{children}</MotionProvider>
      </body>
    </html>
  );
}
