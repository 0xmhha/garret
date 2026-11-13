import { useState, type FormEvent } from "react";
import { Form } from "react-aria-components";
import { useNavigate, useSearchParams } from "react-router";
import { ApiError } from "../api/client";
import { useT } from "../app/context";
import { useSignIn } from "../app/session";
import { Button } from "../ui/Button";
import { TextField } from "../ui/TextField";
import "./SignIn.css";

/** SignIn is the login form; on success it returns to the page that sent here. */
export function SignIn() {
  const t = useT();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const signIn = useSignIn();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");

  const next = safeNext(params.get("next"));
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    signIn.mutate({ username, password }, { onSuccess: () => navigate(next, { replace: true }) });
  };

  return (
    <main className="signin">
      <Form className="signin__card" onSubmit={onSubmit} aria-labelledby="signin-title">
        <div className="signin__head">
          <h1 id="signin-title">{t("signIn.title")}</h1>
          <p className="signin__lede">{t("signIn.lede")}</p>
        </div>
        <TextField label={t("signIn.username")} name="username" value={username} onChange={setUsername} autoComplete="username" isRequired autoFocus />
        <TextField label={t("signIn.password")} name="password" type="password" value={password} onChange={setPassword} autoComplete="current-password" isRequired />
        {signIn.error ? (
          <p className="form-error" role="alert">
            {errorText(signIn.error, t)}
          </p>
        ) : null}
        <Button type="submit" variant="primary" isDisabled={signIn.isPending}>
          {signIn.isPending ? t("signIn.submitting") : t("signIn.submit")}
        </Button>
      </Form>
    </main>
  );
}

function errorText(err: unknown, t: ReturnType<typeof useT>): string {
  if (err instanceof ApiError) {
    if (err.code === "unauthenticated") return t("signIn.failed");
    if (err.code === "network") return t("error.network");
  }
  return t("error.generic");
}

/** safeNext keeps the return path on this site: a path, never another origin. */
export function safeNext(next: string | null): string {
  if (!next || !next.startsWith("/") || next.startsWith("//") || next.includes("\\")) return "/";
  return next;
}
