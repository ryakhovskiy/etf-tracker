"use client";
import { IconStar } from "./icons";

/** The favorites star: the same button on the Manage items tab and on a ticker's page. */
export function StarToggle({ name, on, onToggle, disabled }: { name: string; on: boolean; onToggle: () => void; disabled?: boolean }) {
  return <button type="button" className={`star-toggle ghost${on ? " is-on" : ""}`} data-testid="favorite-toggle" aria-pressed={on} disabled={disabled}
    aria-label={on ? `Remove ${name} from favorites` : `Add ${name} to favorites`} onClick={onToggle}><IconStar size={20} filled={on} /></button>;
}
