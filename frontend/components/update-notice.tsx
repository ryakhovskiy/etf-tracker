"use client";
import { useSyncExternalStore } from "react";
import { applyUpdate, dismissUpdate, getPwaState, SERVER_PWA_STATE, subscribePwa } from "../lib/pwa";

/** "New version available · Reload". Never reloads by itself: unsaved drafts and CSV imports stay safe. */
export function UpdateNotice() {
  const { updateReady, updateDismissed } = useSyncExternalStore(subscribePwa, getPwaState, () => SERVER_PWA_STATE);
  if (!updateReady || updateDismissed) return null;
  return <div className="update-notice" role="status" data-testid="update-notice">
    <span className="update-notice-text">New version available</span>
    <button type="button" className="primary" data-testid="update-reload" onClick={applyUpdate}>Reload</button>
    <button type="button" className="ghost" data-testid="update-dismiss" onClick={dismissUpdate}>Later</button>
  </div>;
}
