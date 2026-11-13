import type { ReactNode } from "react";

/** Inline SVG icons (stroke = currentColor), decorative by default. */
function Icon({ children, size = 20 }: { children: ReactNode; size?: number }) {
  return <svg className="icon" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">{children}</svg>;
}
export const IconDashboard = (p: { size?: number }) => <Icon {...p}><rect x="3" y="3" width="7" height="9" rx="1.5" /><rect x="14" y="3" width="7" height="5" rx="1.5" /><rect x="14" y="12" width="7" height="9" rx="1.5" /><rect x="3" y="16" width="7" height="5" rx="1.5" /></Icon>;
export const IconTransactions = (p: { size?: number }) => <Icon {...p}><path d="M7 4 3 8l4 4" /><path d="M3 8h14" /><path d="m17 20 4-4-4-4" /><path d="M21 16H7" /></Icon>;
export const IconDividends = (p: { size?: number }) => <Icon {...p}><rect x="3" y="5" width="18" height="16" rx="2.5" /><path d="M3 10h18M8 3v4M16 3v4" /><path d="M9 15.5h6" /></Icon>;
export const IconImport = (p: { size?: number }) => <Icon {...p}><path d="M12 3v12" /><path d="m7 10 5 5 5-5" /><path d="M4 19h16" /></Icon>;
export const IconItems = (p: { size?: number }) => <Icon {...p}><path d="M4 6h16M4 12h16M4 18h10" /></Icon>;
export const IconPalette = (p: { size?: number }) => <Icon {...p}><path d="M12 3a9 9 0 1 0 0 18c1.2 0 1.8-.8 1.8-1.7 0-1-.9-1.4-.9-2.4 0-.9.7-1.6 1.7-1.6H17a4 4 0 0 0 4-4C21 6.6 17 3 12 3Z" /><circle cx="7.5" cy="11" r="1" fill="currentColor" stroke="none" /><circle cx="10.5" cy="7" r="1" fill="currentColor" stroke="none" /><circle cx="15.5" cy="7.5" r="1" fill="currentColor" stroke="none" /></Icon>;
export const IconCheck = (p: { size?: number }) => <Icon {...p}><path d="m5 12.5 4.5 4.5L19 7.5" /></Icon>;
export const IconSignOut = (p: { size?: number }) => <Icon {...p}><path d="M9 4H5a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h4" /><path d="m16 8 4 4-4 4" /><path d="M20 12H9" /></Icon>;
export const IconArrowLeft = (p: { size?: number }) => <Icon {...p}><path d="M19 12H5" /><path d="m11 18-6-6 6-6" /></Icon>;
export const IconPlus = (p: { size?: number }) => <Icon {...p}><path d="M12 5v14M5 12h14" /></Icon>;
export const IconChevronDown = (p: { size?: number }) => <Icon {...p}><path d="m6 9 6 6 6-6" /></Icon>;
