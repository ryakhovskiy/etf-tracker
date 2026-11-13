import { dateLabel } from "../lib/performance";
import { euro, units, type TransactionGroupPerformance, type TransactionRow } from "../lib/transactions";
import { TileFields } from "./table-controls";
import { TransactionPerformance, TransactionPerformanceTotal } from "./transaction-performance";

/** Consecutive lines of one transaction form a group (the API returns them ordered). */
export function groupLines(lines: TransactionRow[]) {
  const groups: TransactionRow[][] = [];
  for (const line of lines) {
    const last = groups[groups.length - 1];
    if (last && last[0].transactionId === line.transactionId) last.push(line);
    else groups.push([line]);
  }
  return groups;
}

/** The transaction history: one tile per transaction, each line as a grid of labelled fields. */
export function TransactionGroups({ groups, summaries, labels, visible, disabled, onDelete }: {
  groups: TransactionRow[][];
  summaries?: Record<string, TransactionGroupPerformance>;
  labels: string[];
  visible: number[];
  disabled: boolean;
  onDelete: (line: TransactionRow) => void;
}) {
  return (
    <div className="txn-groups">
      {groups.map(lines => {
        const first = lines[0];
        return (
          <section key={first.transactionId} className="txn-group tile" aria-label={`Transaction ${first.transactionId}`} data-testid="transaction-group">
            <header className="txn-group-head">
              <div className="txn-group-title">
                <h3>Transaction #{first.transactionId} · {dateLabel(first.date)}</h3>
                <TransactionPerformanceTotal summary={summaries?.[first.transactionId]} />
              </div>
              <button className="danger" disabled={disabled} onClick={() => onDelete(first)} aria-label={`Delete transaction ${first.transactionId}`}>Delete</button>
            </header>
            <ul className="txn-lines">
              {lines.map(line => (
                <li key={line.id} className="txn-line" data-testid="transaction-line">
                  <TileFields
                    labels={labels}
                    visible={visible}
                    values={[
                      dateLabel(line.date),
                      line.ticker,
                      line.name,
                      units(line.quantity),
                      `${units(line.price)} €`,
                      euro(line.amount),
                      <TransactionPerformance performance={line.performance} />,
                      <span className="badge">{line.source}</span>,
                    ]}
                  />
                </li>
              ))}
            </ul>
          </section>
        );
      })}
    </div>
  );
}
