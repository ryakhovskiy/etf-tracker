"use client";
import { useEffect, useId, useRef, useState, useSyncExternalStore, type KeyboardEvent } from "react";
import { THEME_LABELS, THEME_PREFERENCES, currentPreference, setThemePreference, subscribeTheme, type ThemePreference } from "../lib/theme";
import { IconCheck, IconPalette } from "./icons";

/** Colour preview of a theme. The tokens are scoped to the element via [data-theme-preview]. */
function Swatch({ value }: { value: ThemePreference }) {
  if (value === "system") {
    return <span className="theme-swatch theme-swatch-system" aria-hidden="true"><span data-theme-preview="light" /><span data-theme-preview="dark" /></span>;
  }
  return <span className="theme-swatch" data-theme-preview={value} aria-hidden="true" />;
}

/**
 * Theme picker for the top bar: a button that opens a radio group of the six
 * choices (System + 5 themes). Arrow keys move and select, Escape closes.
 */
export function ThemeSwitcher() {
  const preference = useSyncExternalStore(subscribeTheme, currentPreference, () => "system" as ThemePreference);
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const panelId = useId();

  useEffect(() => {
    if (!open) return;
    root.current?.querySelector<HTMLElement>('[role="radio"][aria-checked="true"]')?.focus();
    const onPointerDown = (event: PointerEvent) => { if (!root.current?.contains(event.target as Node)) setOpen(false); };
    document.addEventListener("pointerdown", onPointerDown);
    return () => document.removeEventListener("pointerdown", onPointerDown);
  }, [open]);

  const close = () => { setOpen(false); trigger.current?.focus(); };
  const choose = (value: ThemePreference) => setThemePreference(value);
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Escape") { event.stopPropagation(); close(); return; }
    const step = event.key === "ArrowDown" || event.key === "ArrowRight" ? 1 : event.key === "ArrowUp" || event.key === "ArrowLeft" ? -1 : 0;
    if (!step && event.key !== "Home" && event.key !== "End") return;
    event.preventDefault();
    const index = THEME_PREFERENCES.indexOf(preference);
    const next = event.key === "Home" ? 0 : event.key === "End" ? THEME_PREFERENCES.length - 1 : (index + step + THEME_PREFERENCES.length) % THEME_PREFERENCES.length;
    choose(THEME_PREFERENCES[next]);
    root.current?.querySelectorAll<HTMLElement>('[role="radio"]')[next]?.focus();
  };

  return <div className="theme-switcher" ref={root}>
    <button ref={trigger} type="button" className="theme-trigger" data-testid="theme-trigger" aria-expanded={open} aria-controls={open ? panelId : undefined}
      aria-label={`Theme: ${THEME_LABELS[preference]}`} onClick={() => setOpen(value => !value)}>
      <IconPalette /><span className="theme-trigger-label">{THEME_LABELS[preference]}</span>
    </button>
    {open && <div className="theme-panel" id={panelId} role="radiogroup" aria-label="Theme" data-testid="theme-switcher" onKeyDown={onKeyDown}>
      {THEME_PREFERENCES.map(value => <button key={value} type="button" role="radio" aria-checked={preference === value} tabIndex={preference === value ? 0 : -1}
        className="theme-option" data-testid={`theme-option-${value}`} onClick={() => choose(value)}>
        <Swatch value={value} /><span className="theme-option-label">{THEME_LABELS[value]}</span>
        {preference === value && <IconCheck size={16} />}
      </button>)}
    </div>}
  </div>;
}
