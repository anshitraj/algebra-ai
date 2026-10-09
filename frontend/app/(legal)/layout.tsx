import { Nav } from "@/components/nav";
import { Footer } from "@/components/footer";

export default function LegalLayout({ children }: { children: React.ReactNode }) {
  return (
    <>
      <Nav />
      <main className="pt-14 pb-24 md:pt-20">{children}</main>
      <Footer />
    </>
  );
}
