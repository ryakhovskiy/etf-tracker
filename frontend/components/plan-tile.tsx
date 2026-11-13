import { hasPrice, rowProblem, type PlanRow } from "../lib/transactions";
import { SymbolLink } from "./price-fetch";

type Field = "price" | "amount" | "quantity";
const FIELDS: readonly (readonly [Field, string])[] = [["price", "Price €"], ["amount", "Amount €"], ["quantity", "Quantity"]];

/** One instrument of the transaction being entered: fetch toggle, three linked inputs, source and price link. */
export function PlanTile({ row, canFetch, fetchChecked, fetchDisabled, inputsDisabled, onToggleFetch, onEdit, reload }: {
  row: PlanRow;
  canFetch: boolean;
  fetchChecked: boolean;
  fetchDisabled: boolean;
  inputsDisabled: boolean;
  onToggleFetch: (checked: boolean) => void;
  onEdit: (field: Field, value: string) => void;
  reload: () => Promise<unknown>;
}) {
  const noPrice = !hasPrice(row.price);
  const problem = rowProblem(row);
  return (
    <li className="plan-tile tile" data-testid={`plan-row-${row.instrumentId}`} aria-label={`${row.ticker} ${row.name}`}>
      <div className="plan-head">
        <label className="plan-fetch">
          <input type="checkbox" aria-label={`Fetch ${row.ticker}`} disabled={!canFetch || fetchDisabled} checked={canFetch && fetchChecked} onChange={event => onToggleFetch(event.target.checked)} />
          <span aria-hidden="true">Fetch</span>
        </label>
        <div className="plan-title">
          <span className="plan-ticker">{row.ticker}</span>
          <span className="plan-name">{row.name}</span>
        </div>
        {row.type && <span className={`pill ${row.type === "ETF" ? "pill-etf" : "pill-stock"}`}>{row.type === "ETF" ? "ETF" : "Stock"}</span>}
      </div>
      <div className="plan-inputs">
        {FIELDS.map(([field, caption]) => {
          const blocked = field === "quantity" && noPrice;
          return (
            <div key={field} className="plan-field">
              {/* The caption is visual only; the sr-only label carries the accessible name. */}
              <span className="field-caption" aria-hidden="true">{caption}</span>
              <label className="sr-only" htmlFor={`${field}-${row.instrumentId}`}>{row.ticker} {field}</label>
              <input
                id={`${field}-${row.instrumentId}`}
                inputMode="decimal"
                value={blocked ? "" : row[field].replace(".", ",")}
                placeholder={blocked ? "–" : undefined}
                disabled={inputsDisabled || blocked}
                aria-invalid={!!row.errors[field]}
                onChange={event => onEdit(field, event.target.value)}
              />
              {row.errors[field] && <p className="error">{row.errors[field]}</p>}
              {field === "price" && problem && !row.errors.price && <p className="error">{problem}</p>}
            </div>
          );
        })}
      </div>
      <div className="plan-foot">
        <p className="plan-source muted">{row.priceSource}</p>
        <SymbolLink row={row} reload={reload} />
      </div>
    </li>
  );
}
