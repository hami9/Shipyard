import { useState } from "react";

/**
 * CopyButton copies a value to the clipboard and says so on itself for a
 * moment. The clipboard needs a secure context (HTTPS, or localhost); where
 * it is missing, the button is not shown and the value stays selectable.
 */
export function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false);
  if (!navigator.clipboard) {
    return null;
  }
  return (
    <button
      type="button"
      className="small"
      aria-label={copied ? `${label} copied` : `Copy ${label}`}
      onClick={() => {
        void navigator.clipboard.writeText(value).then(() => {
          setCopied(true);
          setTimeout(() => setCopied(false), 2000);
        });
      }}
    >
      {copied ? "Copied" : "Copy"}
    </button>
  );
}
