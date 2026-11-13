import { useState } from "react";
import { Form, Label, TextArea, TextField } from "react-aria-components";
import { ApiError } from "../api/client";
import { useT } from "../app/context";
import { Button } from "../ui/Button";
import { AttachButton, PendingList, useUploads } from "./attachments";

interface Props {
  label: string;
  submitLabel: string;
  /** Receives the text and the IDs of the files uploaded for it. */
  onSubmit: (body: string, attachments: string[]) => Promise<unknown>;
  onCancel?: () => void;
  /** The project files upload to; without it the box takes no files. */
  project?: string;
}

/** Composer writes a comment, with files when given a project; Cmd or Ctrl+Enter sends it. */
export function Composer({ label, submitLabel, onSubmit, onCancel, project }: Props) {
  const t = useT();
  const [body, setBody] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const uploads = useUploads(project ?? "");
  const cancel = onCancel
    ? () => {
        uploads.discard();
        onCancel();
      }
    : undefined;

  const send = async () => {
    if (body.trim() === "" || busy || uploads.busy) return;
    setBusy(true);
    setError("");
    try {
      await onSubmit(body, uploads.ids);
      setBody("");
      uploads.clear();
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
            if (e.key === "Escape" && cancel) cancel();
          }}
        />
      </TextField>
      {project ? <PendingList pending={uploads.pending} onRemove={uploads.remove} /> : null}
      {error ? <p className="form-error" role="alert">{error}</p> : null}
      <div className="composer__actions">
        {project ? <AttachButton onFiles={uploads.add} isDisabled={busy} /> : null}
        <span className="composer__hint">{t("composer.hint")}</span>
        {cancel ? (
          <Button variant="quiet" onPress={cancel}>
            {t("composer.cancel")}
          </Button>
        ) : null}
        <Button type="submit" variant="primary" isDisabled={busy || uploads.busy || body.trim() === ""}>
          {submitLabel}
        </Button>
      </div>
    </Form>
  );
}
