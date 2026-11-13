import { useState } from "react";
import { Form, Label, TextArea, TextField } from "react-aria-components";
import { ApiError } from "../api/client";
import { useT } from "../app/context";
import { Button } from "../ui/Button";

interface Props {
  label: string;
  submitLabel: string;
  onSubmit: (body: string) => Promise<unknown>;
  onCancel?: () => void;
}

/** Composer writes a comment; Cmd or Ctrl+Enter sends it. */
export function Composer({ label, submitLabel, onSubmit, onCancel }: Props) {
  const t = useT();
  const [body, setBody] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const send = async () => {
    if (body.trim() === "" || busy) return;
    setBusy(true);
    setError("");
    try {
      await onSubmit(body);
      setBody("");
    } catch (err) {
      setError(err instanceof ApiError && err.code === "network" ? t("error.network") : t("composer.failed"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Form
      className="composer"
      onSubmit={(e) => {
        e.preventDefault();
        void send();
      }}
    >
      <TextField value={body} onChange={setBody} className="composer__field" aria-label={label}>
        <Label className="visually-hidden">{label}</Label>
        <TextArea
          className="composer__input"
          rows={4}
          placeholder={t("composer.placeholder")}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
              e.preventDefault();
              void send();
            }
            if (e.key === "Escape" && onCancel) onCancel();
          }}
        />
      </TextField>
      {error ? <p className="form-error" role="alert">{error}</p> : null}
      <div className="composer__actions">
        <span className="composer__hint">{t("composer.hint")}</span>
        {onCancel ? (
          <Button variant="quiet" onPress={onCancel}>
            {t("composer.cancel")}
          </Button>
        ) : null}
        <Button type="submit" variant="primary" isDisabled={busy || body.trim() === ""}>
          {submitLabel}
        </Button>
      </div>
    </Form>
  );
}
