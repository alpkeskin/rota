"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

export default function Home() {
  const router = useRouter();

  useEffect(() => {
    const token = localStorage.getItem("auth_token");
    router.replace(token ? "/dashboard" : "/login");
  }, [router]);

  return <p className="text-muted-foreground grid min-h-svh place-items-center">Redirecting…</p>;
}
