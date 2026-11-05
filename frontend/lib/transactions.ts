import Decimal from "decimal.js";
Decimal.set({ precision: 40, rounding: Decimal.ROUND_HALF_UP });
export type PlanItem = { instrumentId: number; ticker: string; name: string; defaultAmount: string; defaultPrice: string; priceSource: string; avSymbol?: string; avCurrency?: string; avRegion?: string; avLinkedByYou?: boolean; avLinkedAt?: string; quoteId?: number };
export type Line = { instrumentId: number; amount: string; price: string; quantity: string; quoteId?: number };
export type PlanRow = PlanItem & Line & { errors: Partial<Record<"amount" | "price" | "quantity", string>> };
export type Performance = { dates: string[]; series: { instrumentId: number; name: string; values: string[] }[]; totals: string[]; invested: string };
export type TransactionRow = Line & { id: number; transactionId: number; date: string; ticker: string; name: string; source: string };
export const today = () => new Intl.DateTimeFormat("sv-SE",{timeZone:"Europe/Berlin",year:"numeric",month:"2-digit",day:"2-digit"}).format(new Date());
export function decimal(raw: string, places: number, zero = false): Decimal {
  const normalized = raw.replace(",", ".");
  if (!new RegExp(`^\\d+(?:\\.\\d{1,${places}})?$`).test(normalized)) throw new Error(`Enter a decimal with up to ${places} places.`);
  const d = new Decimal(normalized);
  if (d.lt(zero ? 0 : "0.0000000001") || d.gt("1000000")) throw new Error("Must be greater than 0 and at most 1,000,000.");
  return d;
}
export const euro = (value: string) => `${new Decimal(value).toFixed(2).replace(/\B(?=(\d{3})+(?!\d))/g,"_").replace(".",",").replaceAll("_",".")} €`;
export const units = (value: string) => { const [whole, fraction] = new Decimal(value).toFixed().split("."); return whole.replace(/\B(?=(\d{3})+(?!\d))/g,".") + (fraction ? `,${fraction}` : ""); };
export function planRow(item: PlanItem): PlanRow {
 return { ...item, amount: item.defaultAmount, price: item.defaultPrice, quantity: item.defaultPrice ? new Decimal(item.defaultAmount).div(item.defaultPrice).toFixed(6) : "", errors: {} };
}
export function editRow(row: PlanRow, field: "amount" | "price" | "quantity", raw: string): PlanRow {
 const next = { ...row, [field]: raw, errors: { ...row.errors } }; delete next.errors[field];
 if (!raw) return next;
 let value: Decimal;
 try { value = decimal(raw, field === "amount" ? 2 : field === "price" ? 4 : 6, field !== "quantity"); }
 catch (e) { next.errors[field] = (e as Error).message; return next; }
 // An absent or invalid dependent field leaves the other fields untouched.
 if (field === "amount" || field === "quantity") { try { decimal(row.price,4,true); } catch { return next; } }
 if (field === "price") { try { decimal(row.amount,2,true); } catch { return next; } }
 try {
  if (field === "quantity") {
   const amount = value.mul(decimal(row.price,4)).toDecimalPlaces(2);
   if (amount.gt("1000000") || amount.eq(0)) throw new Error("Derived amount must be greater than 0 and at most 1,000,000.");
   next.amount = amount.toFixed(2); delete next.errors.amount;
  } else {
   const price = field === "price" ? value : decimal(row.price,4,true);
   const amount = field === "amount" ? value : decimal(row.amount,2,true);
   if (price.eq(0)) return next;
   const quantity = amount.div(price).toDecimalPlaces(6);
   if (quantity.gt("1000000") || (amount.gt(0) && quantity.eq(0))) throw new Error("Derived quantity must be greater than 0 and at most 1,000,000.");
   next.quantity = quantity.toFixed(6); delete next.errors.quantity;
  }
 } catch (e) { next.errors[field] = (e as Error).message; }
 return next;
}
export function rowProblem(row: PlanRow): string {
 try {const a = decimal(row.amount,2,true);if(a.eq(0))return "";decimal(row.price,4);decimal(row.quantity,6);if(Object.keys(row.errors).length)return "Correct the invalid fields.";return "";}catch(e){return (e as Error).message;}
}
export function total(rows: PlanRow[]) { return rows.reduce((sum,row) => {try{return sum.add(decimal(row.amount,2,true));}catch{return sum;}},new Decimal(0)).toFixed(2); }
export function hasPrice(raw: string) { try { return decimal(raw,4,true).gt(0); } catch { return false; } }
export function chartData(performance: Performance) {
 // Recharts needs numeric SVG coordinates; all arithmetic and displayed values retain decimal strings.
 return performance.dates.map((date,i) => Object.fromEntries([["date",Date.parse(`${date}T00:00:00Z`)], ...performance.series.map(s=>[`i${s.instrumentId}`,new Decimal(s.values[i]).toNumber()]),["total",performance.totals[i]]]));
}
export function fetchDateAllowed(date:string,now=new Date()) {
 const berlin=new Intl.DateTimeFormat("sv-SE",{timeZone:"Europe/Berlin",year:"numeric",month:"2-digit",day:"2-digit"}).format(now);
 const previous=new Date(`${berlin}T12:00:00Z`);previous.setUTCDate(previous.getUTCDate()-1);while([0,6].includes(previous.getUTCDay()))previous.setUTCDate(previous.getUTCDate()-1);
 return date===berlin||date===previous.toISOString().slice(0,10);
}
export function fetchedSource(q:{quoteDate?:string;currency?:string;fxRate?:string;fxRateDate?:string}) {
 const date=(d?:string)=>d?.split("-").reverse().join(".")??"";
 return `Fetched ${date(q.quoteDate)}${q.fxRate?` · 1 ${q.currency==="GBX"||q.currency==="GBp"?"GBP":q.currency} = ${new Decimal(q.fxRate).toFixed(4).replace(".",",")} € (ECB ${date(q.fxRateDate)})`:""}`;
}
