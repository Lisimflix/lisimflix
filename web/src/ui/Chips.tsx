import { useEffect, useId, useRef, useState, type KeyboardEvent } from "react";

export interface Option { value: string; label: string }

export function Chip({ label, pressed, onClick }: { label: string; pressed: boolean; onClick: () => void }) {
  return (
    <button type="button" className="chip" aria-pressed={pressed} onClick={onClick}>
      {label}
    </button>
  );
}

// A menu chip: button + listbox. Arrow keys move, Enter picks, Escape closes; picking the chosen option clears it.
export function MenuChip({ label, options, value, onChange }: { label: string; options: Option[]; value: string | null; onChange: (value: string | null) => void }) {
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const button = useRef<HTMLButtonElement>(null);
  const list = useRef<HTMLUListElement>(null);
  const id = useId();
  const selected = options.find((o) => o.value === value);
  useEffect(() => {
    if (open) list.current?.focus();
  }, [open]);

  const close = () => {
    setOpen(false);
    button.current?.focus();
  };
  const pick = (v: string) => {
    onChange(v === value ? null : v);
    close();
  };
  function onKeyDown(e: KeyboardEvent) {
    const last = options.length - 1;
    if (e.key === "Escape") return close();
    if (e.key === "ArrowDown") setActive((a) => Math.min(a + 1, last));
    else if (e.key === "ArrowUp") setActive((a) => Math.max(a - 1, 0));
    else if (e.key === "Home") setActive(0);
    else if (e.key === "End") setActive(last);
    else if ((e.key === "Enter" || e.key === " ") && options[active]) pick(options[active].value);
    else return;
    e.preventDefault();
  }

  return (
    <div className="menuchip" onBlur={(e) => !e.currentTarget.contains(e.relatedTarget) && setOpen(false)}>
      <button
        ref={button}
        type="button"
        className="chip"
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => {
          setActive(Math.max(options.findIndex((o) => o.value === value), 0));
          setOpen(!open);
        }}
      >
        {selected ? `${label}: ${selected.label}` : label}
        <span aria-hidden="true"> ▾</span>
      </button>
      {open && (
        <ul ref={list} role="listbox" aria-label={label} tabIndex={0} aria-activedescendant={`${id}-${active}`} className="listbox" onKeyDown={onKeyDown}>
          {options.map((o, i) => (
            <li key={o.value} id={`${id}-${i}`} role="option" aria-selected={o.value === value} data-active={i === active} onClick={() => pick(o.value)}>
              {o.label}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
