"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { Spinner } from "@/components/icons";

/** Passes live on the Spend firewall page now; old links (and #pass-<id> anchors) land there. */
export default function PassesRedirect() {
  const router = useRouter();
  useEffect(() => {
    router.replace(`/console/firewall${window.location.search}${window.location.hash}`);
  }, [router]);
  return (
    <div className="flex min-h-[40vh] items-center justify-center text-muted" aria-busy="true">
      <Spinner size={20} />
    </div>
  );
}
