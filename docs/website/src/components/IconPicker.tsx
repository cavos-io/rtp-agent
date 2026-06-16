import React from 'react';

type IconPickerFieldProps = {
  input?: {
    value?: string;
    onChange?: (value: string) => void;
  };
};

export function IconPickerField({input}: IconPickerFieldProps): React.ReactElement {
  return (
    <input
      type="text"
      value={input?.value ?? ''}
      onChange={(event) => input?.onChange?.(event.target.value)}
    />
  );
}
