"use client";
import { useEffect, useRef } from "react";

/** Dialog (centered card, bottom sheet on phones). Kept out of portfolios.tsx so the portfolio pages do not pull in the start page and its drag-and-drop code. */
export function Modal({ title, close, children, testId }: { title: string; close: () => void; children: React.ReactNode; testId?: string }) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => { const dialog = ref.current!; dialog.showModal(); return () => dialog.close(); }, []);
  return <dialog ref={ref} aria-label={title} data-testid={testId} onCancel={e => { e.preventDefault(); close(); }} onClick={e => { if (e.target === e.currentTarget) close(); }}><h2>{title}</h2>{children}</dialog>;
}
