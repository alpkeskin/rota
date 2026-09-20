"use client";

import { useState, useEffect, Suspense } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import Image from "next/image";
import { AlertCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";

function LoginForm() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState("");
  const [sessionExpired, setSessionExpired] = useState(false);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");

  // Check if already logged in or redirected due to expired session
  useEffect(() => {
    if (searchParams.get("reason") === "session_expired") {
      setSessionExpired(true);
    } else {
      const token = localStorage.getItem("auth_token");
      if (token) {
        router.push("/dashboard");
      }
    }
  }, [router, searchParams]);

  const handleSubmit = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    setIsLoading(true);
    setError("");

    try {
      await api.login(username, password);
      router.push("/dashboard");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Invalid credentials. Please try again.");
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <div className="grid min-h-svh place-items-center px-6 py-16">
      <div className="flex w-full max-w-[19rem] flex-col items-center text-center">
        <Image src="/logo.png" alt="" width={44} height={44} className="size-11 object-contain" priority />
        <h1 className="mt-5 text-[1.0625rem] leading-tight font-semibold tracking-tight">Sign in to Rota</h1>
        <p className="text-muted-foreground mt-1">Admin credentials for this instance.</p>

        <form onSubmit={handleSubmit} className="mt-8 w-full space-y-3 text-left">
          <div className="space-y-1.5">
            <Label htmlFor="username">Username</Label>
            <Input
              id="username"
              type="text"
              required
              autoComplete="username"
              autoFocus
              disabled={isLoading}
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              className="h-9 px-3"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="password">Password</Label>
            <Input
              id="password"
              type="password"
              required
              autoComplete="current-password"
              disabled={isLoading}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="h-9 px-3"
            />
          </div>

          {sessionExpired && !error && (
            <p className="text-muted-foreground flex items-start gap-1.5 pt-0.5">
              <AlertCircle className="mt-0.5 size-3.5 shrink-0" aria-hidden />
              Your session expired. Sign in again to continue.
            </p>
          )}
          {error && (
            <p role="alert" className="text-critical flex items-start gap-1.5 pt-0.5">
              <AlertCircle className="mt-0.5 size-3.5 shrink-0" aria-hidden />
              {error}
            </p>
          )}

          <Button type="submit" size="lg" className="mt-5 w-full" disabled={isLoading}>
            {isLoading ? "Signing in…" : "Sign in"}
          </Button>
        </form>
      </div>
    </div>
  );
}

export default function LoginPage() {
  return (
    <div className="dark min-h-svh" style={{ background: "#0E0E0E", color: "oklch(0.985 0 0)" }}>
      <Suspense>
        <LoginForm />
      </Suspense>
    </div>
  );
}
