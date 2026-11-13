import { useEffect, useRef, useState } from "react";
import { useT } from "../app/context";
import { Button } from "./Button";

interface Props {
  /** The action, such as "Revoke". */
  label: string;
  /** What happens, asked before it does, such as "Revoke this token?". */
  prompt: string;
  /** The confirming button; defaults to label. */
  confirmLabel?: string;
  /** Names the action for screen readers when label alone is ambiguous in a list. */
  "aria-label"?: string | undefined;
  isDisabled?: boolean | undefined;
  onConfirm: () => void;
}

/**
 * ConfirmButton asks once before a destructive action: the first press
 * swaps the button for the prompt, a confirming button and Cancel, with
 * focus on Cancel so a second Enter does not confirm by accident.
 */
export function ConfirmButton({ label, prompt, confirmLabel, isDisabled, onConfirm, ...rest }: Props) {
  const t = useT();
  const [asking, setAsking] = useState(false);
  const cancel = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (asking) cancel.current?.focus();
  }, [asking]);
  if (!asking) {
    return (
      <Button {...(rest["aria-label"] ? { "aria-label": rest["aria-label"] } : {})} isDisabled={isDisabled ?? false} onPress={() => setAsking(true)}>
        {label}
      </Button>
    );
  }
  return (
    <span className="confirm" role="group" aria-label={prompt}>
      <span className="confirm__prompt">{prompt}</span>
      <Button
        variant="danger"
        onPress={() => {
          setAsking(false);
          onConfirm();
        }}
      >
        {confirmLabel ?? label}
      </Button>
      <Button ref={cancel} variant="quiet" onPress={() => setAsking(false)}>
        {t("app.cancel")}
      </Button>
    </span>
  );
}
