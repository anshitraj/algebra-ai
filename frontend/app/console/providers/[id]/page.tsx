import { ProviderDetailPage } from "@/components/console/provider-detail";

// A provider ID has a colon ("payai:…"), which reaches this page still
// percent-encoded. Decode it once here, or the API client encodes it twice
// and every provider answers "no such provider".
function decoded(id: string) {
  try {
    return decodeURIComponent(id);
  } catch {
    return id;
  }
}

export default async function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <ProviderDetailPage id={decoded(id)} />;
}
