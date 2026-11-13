import { FieldError, Input, Label, Text, TextField as AriaTextField, type TextFieldProps } from "react-aria-components";

/** TextField is a labelled input with an optional hint and its error under it. */
export function TextField({ label, description, error, ...rest }: TextFieldProps & { label: string; description?: string; error?: string }) {
  return (
    <AriaTextField {...rest} className="field">
      <Label className="field__label">{label}</Label>
      <Input className="field__input" />
      {description ? (
        <Text slot="description" className="field__hint">
          {description}
        </Text>
      ) : null}
      <FieldError className="field__error">{error}</FieldError>
    </AriaTextField>
  );
}
