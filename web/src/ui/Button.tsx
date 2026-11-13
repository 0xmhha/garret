import type { Ref } from "react";
import { Button as AriaButton, type ButtonProps } from "react-aria-components";

type Variant = "default" | "primary" | "quiet" | "danger";

/** Button is the one button style; variant picks emphasis, and danger marks an action that cannot be undone. */
export function Button({ variant = "default", className, ...rest }: ButtonProps & { variant?: Variant; ref?: Ref<HTMLButtonElement> }) {
  const cls = ["btn", variant === "default" ? "" : `btn--${variant}`, typeof className === "string" ? className : ""]
    .filter(Boolean)
    .join(" ");
  return <AriaButton {...rest} className={cls} />;
}
