import { FieldError, Input, Label, TextField as AriaTextField, type TextFieldProps } from "react-aria-components";

/** TextField is a labelled input with its error under it. */
export function TextField({ label, error, ...rest }: TextFieldProps & { label: string; error?: string }) {
  return (
    <AriaTextField {...rest} className="field">
      <Label className="field__label">{label}</Label>
      <Input className="field__input" />
      <FieldError className="field__error">{error}</FieldError>
    </AriaTextField>
  );
}
