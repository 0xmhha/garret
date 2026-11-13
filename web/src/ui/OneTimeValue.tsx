import { useState } from "react";
import { useT } from "../app/context";
import { Button } from "./Button";

interface Props {
  label: string;
  value: string;
}

/**
 * OneTimeValue shows a value the server returns once, such as a token
 * secret or an invitation code, with a copy button. The caller decides
 * when it disappears.
 */
export function OneTimeValue({ label, value }: Props) {
  const t = useT();
  const [copied, setCopied] = useState<"" | "done" | "failed">("");
  return (
    <div className="onetime">
      <span className="onetime__head">
        <span className="onetime__label">{label}</span>
        <span className="onetime__status" role="status">
          {copied === "done" ? t("app.copied") : copied === "failed" ? t("app.copyFailed") : ""}
        </span>
      </span>
      <code className="onetime__value">{value}</code>
      <Button
        onPress={() => {
          navigator.clipboard.writeText(value).then(
            () => setCopied("done"),
            () => setCopied("failed"),
          );
        }}
      >
        {t("app.copy")}
      </Button>
    </div>
  );
}
