import { Nav } from "@/components/nav";
import { Hero } from "@/components/hero";
import { ProviderStrip } from "@/components/provider-strip";
import { GetStarted } from "@/components/get-started";
import { DashboardSection } from "@/components/dashboard-section";
import { HowItWorks } from "@/components/how-it-works";
import { PolicyDimensions } from "@/components/policy-dimensions";
import { TrustBoundaries } from "@/components/trust-boundaries";
import { Providers } from "@/components/providers-section";
import { IntegrateSection } from "@/components/integrate-section";
import { Footer } from "@/components/footer";

export default function Home() {
  return (
    <>
      <Nav />
      <main>
        <Hero />
        <ProviderStrip />
        <GetStarted />
        <DashboardSection />
        <HowItWorks />
        <PolicyDimensions />
        <TrustBoundaries />
        <Providers />
        <IntegrateSection />
      </main>
      <Footer />
    </>
  );
}
