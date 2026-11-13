"use client";
import { Children, useState, type ReactNode } from "react";

export function useTableColumns(labels: string[], text = { summary: "Columns", reset: "Reset columns" }) {
 const [order, setOrder] = useState(() => labels.map((_, index) => index));
 const [hidden, setHidden] = useState<number[]>([]);
 const visible = order.filter(index => !hidden.includes(index));
 const move = (index: number, offset: number) => setOrder(current => {
  const next = [...current], position = next.indexOf(index), target = position + offset;
  if (target < 0 || target >= next.length) return current;
  [next[position], next[target]] = [next[target], next[position]];
  return next;
 });
 const controls = <details className="table-columns"><summary>{text.summary}</summary><div className="column-options">
  {order.map((index, position) => <div className="column-option" key={index}>
   <label><input type="checkbox" checked={!hidden.includes(index)} disabled={visible.length === 1 && visible[0] === index} onChange={event => setHidden(current => event.target.checked ? current.filter(value => value !== index) : [...current, index])}/>{labels[index]}</label>
   <button type="button" aria-label={`Move ${labels[index]} left`} disabled={position === 0} onClick={() => move(index, -1)}>←</button>
   <button type="button" aria-label={`Move ${labels[index]} right`} disabled={position === order.length - 1} onClick={() => move(index, 1)}>→</button>
  </div>)}
  <button type="button" onClick={() => {setOrder(labels.map((_, index) => index));setHidden([]);}}>{text.reset}</button>
 </div></details>;
 return {visible, controls, headers: visible.map(index => <th scope="col" key={index}>{labels[index]}</th>)};
}

export function TableCells({visible, children}: {visible: number[]; children: ReactNode}) {
 const cells = Children.toArray(children);
 return <>{visible.map(index => cells[index])}</>;
}

export function PageSize({value, onChange, disabled = false}: {value: number; onChange: (value: number) => void; disabled?: boolean}) {
 return <label className="page-size">Rows per page<select value={value} disabled={disabled} onChange={event => onChange(Number(event.target.value))}>
  {[10, 25, 50, 100, 250].map(size => <option key={size} value={size}>{size}</option>)}
 </select></label>;
}


/** The visible fields of one tile, in the order the user chose: label above value. */
export function TileFields({labels, visible, values}: {labels: string[]; visible: number[]; values: ReactNode[]}) {
 return <dl className="tile-fields">{visible.map(index => <div key={index} className="tile-field" data-field={labels[index]}><dt>{labels[index]}</dt><dd>{values[index]}</dd></div>)}</dl>;
}
