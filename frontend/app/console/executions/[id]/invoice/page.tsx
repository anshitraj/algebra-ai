import { Invoice } from "@/components/console/invoice";

function decoded(id: string) {
  try {
    return decodeURIComponent(id);
  } catch {
    return id;
  }
}

export default async function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <Invoice intentId={decoded(id)} />;
}
