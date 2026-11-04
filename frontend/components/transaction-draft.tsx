"use client";

import { createContext, useContext, useState, type Dispatch, type ReactNode, type SetStateAction } from "react";
import { today, type PlanRow } from "../lib/transactions";

type TransactionDraft = { date: string; rows: PlanRow[] | null };
const TransactionDraftContext = createContext<{
  draft: TransactionDraft;
  setDraft: Dispatch<SetStateAction<TransactionDraft>>;
} | null>(null);

// The portfolio layout stays mounted while navigating between its tabs.
export function TransactionDraftProvider({ children }: { children: ReactNode }) {
  const [draft, setDraft] = useState<TransactionDraft>(() => ({ date: today(), rows: null }));
  return <TransactionDraftContext.Provider value={{ draft, setDraft }}>{children}</TransactionDraftContext.Provider>;
}

export function useTransactionDraft() {
  const context = useContext(TransactionDraftContext);
  if (!context) throw new Error("Transactions must be rendered inside TransactionDraftProvider.");
  return context;
}
