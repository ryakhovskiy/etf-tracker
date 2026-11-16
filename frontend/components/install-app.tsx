"use client";
import { useState, useSyncExternalStore } from "react";
import { getPwaState, promptInstall, SERVER_PWA_STATE, subscribePwa } from "../lib/pwa";
import { IconInstall, IconShare } from "./icons";
import { Modal } from "./portfolios";

/**
 * "Install app" in the header's account area. It appears only where it can work: after the browser's
 * beforeinstallprompt (Chrome, Edge, Android), or as a how-to on iPhone/iPad Safari. It is hidden when
 * the app already runs standalone and after it was installed.
 */
export function InstallApp() {
  const { canInstall, iosHint, installed } = useSyncExternalStore(subscribePwa, getPwaState, () => SERVER_PWA_STATE);
  const [hintOpen, setHintOpen] = useState(false);
  if (installed || (!canInstall && !iosHint)) return null;
  return <>
    <button type="button" className="ghost install-app" data-testid="install-app" onClick={() => { if (canInstall) void promptInstall(); else setHintOpen(true); }}>
      <IconInstall size={18} /><span>Install app</span>
    </button>
    {hintOpen && <Modal title="Add to Home Screen" testId="ios-install-hint" close={() => setHintOpen(false)}>
      <p className="install-hint-step"><IconShare size={22} /><span>Tap Share, then Add to Home Screen</span></p>
      <p className="muted">Holdings then opens like an app, without the browser bars.</p>
      <div className="dialog-actions"><button type="button" className="primary" onClick={() => setHintOpen(false)}>Close</button></div>
    </Modal>}
  </>;
}
