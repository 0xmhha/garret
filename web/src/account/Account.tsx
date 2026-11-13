import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Form } from "react-aria-components";
import { keys } from "../api/keys";
import { useApi, useLocale, useSetLocale, useT } from "../app/context";
import { errorMessage } from "../app/errors";
import { formatDate, formatRelative, type Locale, type MessageKey } from "../i18n/i18n";
import { Button } from "../ui/Button";
import { ConfirmButton } from "../ui/ConfirmButton";
import { OneTimeValue } from "../ui/OneTimeValue";
import { TextField } from "../ui/TextField";
import { certificateStatus, tokenStatus, type Certificate, type Token } from "./model";
import "./settings.css";

/** Account holds the signed-in user's language, access tokens and certificates. */
export function Account() {
  const t = useT();
  return (
    <main className="page settings">
      <h1 className="page__title">{t("account.title")}</h1>
      <Language />
      <Tokens />
      <Certificates />
    </main>
  );
}

const languages: readonly { locale: Locale; name: string }[] = [
  { locale: "en", name: "English" },
  { locale: "ko", name: "한국어" },
];

function Language() {
  const t = useT();
  const locale = useLocale();
  const setLocale = useSetLocale();
  return (
    <section className="settings__section" aria-labelledby="language-title">
      <h2 id="language-title">{t("account.language")}</h2>
      <fieldset className="choice">
        <legend className="visually-hidden">{t("account.language")}</legend>
        {languages.map((l) => (
          <label key={l.locale} className="choice__option">
            <input type="radio" name="language" value={l.locale} checked={locale === l.locale} onChange={() => setLocale(l.locale)} />
            <span lang={l.locale}>{l.name}</span>
          </label>
        ))}
      </fieldset>
    </section>
  );
}

function Tokens() {
  const t = useT();
  const locale = useLocale();
  const api = useApi();
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [created, setCreated] = useState<{ name: string; secret: string } | null>(null);
  const list = useQuery({
    queryKey: keys.tokens(),
    queryFn: ({ signal }) => api.request<{ tokens: Token[] }>("GET", "/api/v1/me/tokens", undefined, { signal }),
    select: (r) => r.tokens,
  });
  const refresh = () => qc.invalidateQueries({ queryKey: keys.tokens() });
  const create = useMutation({
    mutationFn: (n: string) => api.request<{ token: Token; secret: string }>("POST", "/api/v1/me/tokens", { name: n }),
    onSuccess: (r) => {
      setCreated({ name: r.token.name, secret: r.secret });
      setName("");
      void refresh();
    },
  });
  const revoke = useMutation({
    mutationFn: (id: string) => api.request("DELETE", `/api/v1/me/tokens/${encodeURIComponent(id)}`),
    onSettled: refresh,
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    setCreated(null);
    create.mutate(name.trim());
  };
  const now = new Date();

  return (
    <section className="settings__section" aria-labelledby="tokens-title">
      <h2 id="tokens-title">{t("account.tokens")}</h2>
      <p className="muted">{t("account.tokensLede")}</p>
      {created ? (
        <div className="reveal" role="status">
          <p>{t("account.tokenCreated", { name: created.name })}</p>
          <OneTimeValue label={t("account.tokenSecret")} value={created.secret} />
          <Button variant="quiet" onPress={() => setCreated(null)}>
            {t("app.done")}
          </Button>
        </div>
      ) : null}
      <Form className="inline-form" onSubmit={onSubmit}>
        <TextField label={t("account.tokenName")} value={name} onChange={setName} isRequired maxLength={100} description={t("account.tokenNameHint")} />
        <Button type="submit" isDisabled={create.isPending || name.trim() === ""}>
          {t("account.tokenCreate")}
        </Button>
      </Form>
      {create.error ? <p className="form-error" role="alert">{errorMessage(create.error, t)}</p> : null}
      {revoke.error ? <p className="form-error" role="alert">{errorMessage(revoke.error, t)}</p> : null}
      {list.isError ? <p className="form-error" role="alert">{errorMessage(list.error, t)}</p> : null}
      {list.data?.length === 0 ? <p className="muted">{t("account.tokensEmpty")}</p> : null}
      {list.data && list.data.length > 0 ? (
        <div className="table-scroll">
          <table className="table">
            <thead>
              <tr>
                <th scope="col">{t("account.tokenName")}</th>
                <th scope="col">{t("table.created")}</th>
                <th scope="col">{t("account.lastUsed")}</th>
                <th scope="col">{t("table.status")}</th>
                <th scope="col">
                  <span className="visually-hidden">{t("table.actions")}</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {list.data.map((tok) => {
                const status = tokenStatus(tok);
                return (
                  <tr key={tok.id}>
                    <td>
                      <span className="table__strong">{tok.name}</span>
                    </td>
                    <td>
                      <time dateTime={tok.created_at}>{formatDate(locale, new Date(tok.created_at))}</time>
                    </td>
                    <td>{tok.last_used_at ? <time dateTime={tok.last_used_at}>{formatRelative(locale, new Date(tok.last_used_at), now)}</time> : <span className="muted">{t("account.neverUsed")}</span>}</td>
                    <td>
                      <span className={`pill pill--${status}`}>{t(`status.${status}`)}</span>
                    </td>
                    <td className="table__action">
                      {status === "active" ? (
                        <ConfirmButton
                          label={t("app.revoke")}
                          aria-label={t("account.tokenRevokeLabel", { name: tok.name })}
                          prompt={t("account.tokenRevokePrompt", { name: tok.name })}
                          isDisabled={revoke.isPending}
                          onConfirm={() => revoke.mutate(tok.id)}
                        />
                      ) : null}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : null}
    </section>
  );
}

const issuedVia: Readonly<Record<string, MessageKey>> = {
  invitation: "account.via.invitation",
  renewal: "account.via.renewal",
  setup: "account.via.setup",
};

function Certificates() {
  const t = useT();
  const locale = useLocale();
  const api = useApi();
  const qc = useQueryClient();
  const list = useQuery({
    queryKey: keys.certificates(),
    queryFn: ({ signal }) => api.request<{ certificates: Certificate[] }>("GET", "/api/v1/me/certificates", undefined, { signal }),
    select: (r) => r.certificates,
  });
  const revoke = useMutation({
    mutationFn: (serial: string) => api.request("DELETE", `/api/v1/me/certificates/${encodeURIComponent(serial)}`),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.certificates() }),
  });
  const now = new Date();

  return (
    <section className="settings__section" aria-labelledby="certs-title">
      <h2 id="certs-title">{t("account.certificates")}</h2>
      <p className="muted">{t("account.certificatesLede")}</p>
      {revoke.error ? <p className="form-error" role="alert">{errorMessage(revoke.error, t)}</p> : null}
      {list.isError ? <p className="form-error" role="alert">{errorMessage(list.error, t)}</p> : null}
      {list.data?.length === 0 ? <p className="muted">{t("account.certificatesEmpty")}</p> : null}
      {list.data && list.data.length > 0 ? (
        <div className="table-scroll">
          <table className="table">
            <thead>
              <tr>
                <th scope="col">{t("account.device")}</th>
                <th scope="col">{t("account.issuedVia")}</th>
                <th scope="col">{t("account.validUntil")}</th>
                <th scope="col">{t("table.status")}</th>
                <th scope="col">
                  <span className="visually-hidden">{t("table.actions")}</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {list.data.map((c) => {
                const status = certificateStatus(c, now);
                const device = c.device_name || t("account.unnamedDevice");
                const via = issuedVia[c.issued_via];
                return (
                  <tr key={c.serial}>
                    <td>
                      <span className="table__strong">{device}</span>
                      <code className="table__sub">{c.serial}</code>
                    </td>
                    <td>{via ? t(via) : c.issued_via}</td>
                    <td>
                      <time dateTime={c.not_after}>{formatDate(locale, new Date(c.not_after))}</time>
                    </td>
                    <td>
                      <span className={`pill pill--${status}`}>{t(`status.${status}`)}</span>
                    </td>
                    <td className="table__action">
                      {status === "active" || status === "current" ? (
                        <ConfirmButton
                          label={t("app.revoke")}
                          aria-label={t("account.certRevokeLabel", { name: device })}
                          prompt={t(status === "current" ? "account.certRevokeCurrent" : "account.certRevokePrompt", { name: device })}
                          isDisabled={revoke.isPending}
                          onConfirm={() => revoke.mutate(c.serial)}
                        />
                      ) : null}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : null}
    </section>
  );
}
