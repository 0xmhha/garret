import { Button as AriaButton, type ButtonProps } from "react-aria-components";

type Variant = "default" | "primary" | "quiet";

/** Button is the one button style; variant picks emphasis, never color meaning. */
export function Button({ variant = "default", className, ...rest }: ButtonProps & { variant?: Variant }) {
  const cls = ["btn", variant === "default" ? "" : `btn--${variant}`, typeof className === "string" ? className : ""]
    .filter(Boolean)
    .join(" ");
  return <AriaButton {...rest} className={cls} />;
}
