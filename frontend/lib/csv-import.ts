import Decimal from "decimal.js";
export type Mapping = { header: string } | { index: number } | null;
export type CSVSettings = {
 encoding: string; delimiter: string; quoteChar: string; headerRow: boolean; skipLines: number;
 decimalSeparator: string; thousandsSeparator: string; dateFormat: string; stripEuro: boolean;
 mapping: Record<string, Mapping>;
};
export const fieldLabels = { isin:"ISIN", wkn:"WKN", ticker:"Symbol", name:"Name", type:"Type", quantity:"Amount", price:"Price €", total:"Total €", date:"Date" };
export type ImportFields = Record<keyof typeof fieldLabels,string>;
export type ImportRow = {rowNumber:number;include:boolean;fields:ImportFields;decision?:string;status:string;errors:Record<string,string>;warnings:string[];instrument?:import("./api").Instrument;raw?:string[]};
export type ImportSummary = {rows:number;included:number;errors:number;undecidedDuplicates:number;newInstruments:number;newItems:number;transactions:number;lines:number;itemsAfter:number;itemLimit:number};
export type Preview = {headers?:string[];rows:ImportRow[];summary:ImportSummary;revision:string};
export type SettingsResult = {settings:CSVSettings;saved:boolean;updatedAt?:string};
export function defaultSettings():CSVSettings {return {encoding:"auto",delimiter:";",quoteChar:'"',headerRow:true,skipLines:0,decimalSeparator:",",thousandsSeparator:".",dateFormat:"DD.MM.YYYY",stripEuro:true,mapping:Object.fromEntries(Object.entries({isin:"ISIN",wkn:"WKN",ticker:"Symbol",name:"Name",quantity:"Amount",price:"Open Price",date:"Date",total:"Total Invest"}).map(([f,header])=>[f,{header}]))};}
export const same = (a:unknown,b:unknown):boolean => {
 if(a===b)return true;if(!a||!b||typeof a!=="object"||typeof b!=="object")return false;
 const left=a as Record<string,unknown>,right=b as Record<string,unknown>;const keys=Object.keys(left);return keys.length===Object.keys(right).length&&keys.every(k=>same(left[k],right[k]));
};
export function editImportFields(fields:ImportFields,field:keyof ImportFields,raw:string):ImportFields {
 const next={...fields,[field]:raw};
 if(["quantity","price","total"].includes(field)) {
  const normalized=raw.replaceAll(".","").replace(",",".").replace(/\s/g,"");next[field]=normalized;
  try {
   const value=new Decimal(normalized),price=new Decimal(next.price),quantity=new Decimal(next.quantity);
   if(!/^\d+(\.\d+)?$/.test(normalized)||!value.isPositive())return next;
   if(field==="total"&&price.gt(0))next.quantity=value.div(price).toFixed(6,Decimal.ROUND_HALF_UP);
   if(field!=="total"&&price.gt(0)&&quantity.gt(0))next.total=quantity.mul(price).toFixed(2,Decimal.ROUND_HALF_UP);
  }catch{/* Preserve invalid input for server validation. */}
 }
 if(field==="date") {const m=/^(\d{2})\.(\d{2})\.(\d{4})$/.exec(raw);if(m)next.date=`${m[3]}-${m[2]}-${m[1]}`;}
 return next;
}
export function displayImportField(field:keyof ImportFields,value:string):string {
 if(field==="date"&&/^\d{4}-\d{2}-\d{2}$/.test(value))return value.split("-").reverse().join(".");
 if(["quantity","price","total"].includes(field)&&/^\d+(\.\d+)?$/.test(value)) {
  const [whole,fraction]=value.split(".");return whole.replace(/\B(?=(\d{3})+(?!\d))/g,".")+(fraction?","+fraction:"");
 }return value;
}
export function canImport(preview:Preview|null,pending:boolean) {return !!preview&&!pending&&preview.summary.lines>0&&preview.summary.undecidedDuplicates===0&&preview.summary.itemsAfter<=preview.summary.itemLimit&&!preview.rows.some(r=>r.include&&r.status==="ERROR");}
export function importSummary(s:ImportSummary) {return `Import ${s.lines} rows into ${s.transactions} transactions, create ${s.newInstruments} instruments, add ${s.newItems} portfolio items`;}
export const rowPayload=(rows:ImportRow[])=>rows.map(({rowNumber,include,fields,decision})=>({rowNumber,include,fields,decision}));
