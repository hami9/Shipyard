import { useEffect, useRef, useState, type KeyboardEvent, type RefObject } from "react";

// Keyboard and focus behavior shared by the pages (P6.6c manual pass).

/**
 * ConfirmButton is a button that asks once more in place: the confirmation
 * takes focus on its Cancel (the safe choice), Escape cancels, and focus
 * comes back to the button, so a keyboard user never loses their place.
 */
export function ConfirmButton(props: {
  label: string;
  confirmLabel: string;
  onConfirm: () => void;
  destructive?: boolean;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const cancel = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (open) {
      cancel.current?.focus();
    }
  }, [open]);
  const close = () => {
    setOpen(false);
    requestAnimationFrame(() => trigger.current?.focus());
  };

  if (!open) {
    return (
      <button type="button" ref={trigger} onClick={() => setOpen(true)} disabled={props.disabled}>
        {props.label}
      </button>
    );
  }
  return (
    <span className="actions" onKeyDown={(e) => e.key === "Escape" && close()}>
      <button
        type="button"
        className={props.destructive ? "destructive" : undefined}
        onClick={() => {
          setOpen(false);
          props.onConfirm();
        }}
      >
        {props.confirmLabel}
      </button>
      <button type="button" ref={cancel} onClick={close}>
        Cancel
      </button>
    </span>
  );
}

/** focusHeading moves focus to the page's heading: after what had focus (a removed row) is gone. */
export function focusHeading(): void {
  requestAnimationFrame(() => {
    const h = document.querySelector<HTMLElement>("main h1");
    if (h) {
      h.tabIndex = -1;
      h.focus();
    }
  });
}

/**
 * useDialogFocus gives a panel that opens in the page (a confirmation, a
 * small form) dialog behavior: it takes focus when it opens, Escape closes
 * it, and focus returns to what had it before.
 */
export function useDialogFocus<T extends HTMLElement>(
  onEscape: () => void,
  first?: RefObject<HTMLElement | null>,
): { ref: RefObject<T | null>; onKeyDown: (e: KeyboardEvent) => void; tabIndex: -1 } {
  const ref = useRef<T>(null);
  useEffect(() => {
    const before = document.activeElement as HTMLElement | null;
    (first?.current ?? ref.current)?.focus();
    return () => {
      if (before?.isConnected) {
        before.focus();
      }
    };
  }, []); // once: when the panel opens
  return {
    ref,
    tabIndex: -1,
    onKeyDown: (e) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        onEscape();
      }
    },
  };
}
