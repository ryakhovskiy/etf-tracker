"use client";
import {
  DndContext, DragOverlay, KeyboardSensor, MouseSensor, TouchSensor, closestCenter, useSensor, useSensors,
  type Announcements, type DragEndEvent, type DragStartEvent,
} from "@dnd-kit/core";
import { SortableContext, arrayMove, rectSortingStrategy, sortableKeyboardCoordinates, useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import Link from "next/link";
import { useCallback, useEffect, useRef, useState, type MouseEvent } from "react";
import { api, APIError } from "../lib/api";
import { dateLabel } from "../lib/performance";
import { sourceLabel, type SummaryAsOf } from "../lib/portfolio-summary";
import { favoriteChange, favoriteIds, favoriteLabel, favoritePrice, favoritesAsOf, reorderFavorites, type Favorite, type FavoritesResponse } from "../lib/favorites";
import { timeLabel } from "./portfolio-metrics";

/** Hold ~300 ms before a drag starts, so a normal swipe still scrolls the page. */
const ACTIVATION = { delay: 300, tolerance: 8 };
export const REORDER_INSTRUCTIONS = "To reorder, press Space to lift this favorite, use the arrow keys to move it, press Space to drop it or Escape to cancel.";

/** "as of 14:32 · Yahoo real-time · fetched 14:33" */
function asOfLabel({ asOf, fetchedAt }: { asOf: SummaryAsOf; fetchedAt: string }) {
  const when = asOf.quoteTime ? timeLabel(asOf.quoteTime) : dateLabel(asOf.quoteDate);
  const source = sourceLabel(asOf);
  return `as of ${when}${source ? ` · ${source}` : ""} · fetched ${timeLabel(fetchedAt)}`;
}

function TileBody({ favorite }: { favorite: Favorite }) {
  const change = favoriteChange(favorite.change);
  return <>
    <span className="favorite-name" data-testid="favorite-name" title={favorite.name}>{favorite.name}</span>
    <span className="favorite-price num" data-testid="favorite-price">{favoritePrice(favorite.priceEur)}</span>
    <span className={`favorite-change num performance-${change.tone}`} data-testid="favorite-change">
      {favorite.change && <span className="sr-only">Change vs previous close: </span>}{change.text}
    </span>
  </>;
}

function SortableTile({ favorite, disabled, justDragged }: { favorite: Favorite; disabled: boolean; justDragged: { current: boolean } }) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: favorite.instrumentId, disabled });
  // A tap opens the title's page; a long-press drag that ends on the tile must not.
  const open = (event: MouseEvent) => { if (justDragged.current || isDragging) event.preventDefault(); };
  return <li ref={setNodeRef} className="favorite-cell" style={{ transform: CSS.Transform.toString(transform), transition }}>
    <Link {...attributes} {...listeners} href={`/instruments/${favorite.instrumentId}`} role={undefined} aria-pressed={undefined} draggable={false} prefetch={false}
      className={`favorite-tile tile${isDragging ? " is-placeholder" : ""}`} data-testid="favorite-tile" data-instrument-id={favorite.instrumentId}
      aria-label={favoriteLabel(favorite)} onClick={open} onContextMenu={event => event.preventDefault()}>
      <TileBody favorite={favorite} />
    </Link>
  </li>;
}

/**
 * The user's starred titles as compact tiles (name, price, change vs previous close), in the user's
 * order. Reorder by long-press and drag, or with the keyboard (Space, arrows, Space).
 * `updating` is the start page's price refresh: when it ends the tiles reload with the new prices.
 * `reloadKey` forces a reload (e.g. after a portfolio was deleted, which can drop favorites).
 */
export function Favorites({ updating, reloadKey = 0, onCountChange }: { updating: boolean; reloadKey?: number; onCountChange?: (count: number) => void }) {
  const [items, setItems] = useState<Favorite[] | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [saving, setSaving] = useState(false);
  const [active, setActive] = useState<Favorite | null>(null);
  const version = useRef(0);
  const busy = useRef(false);
  const stale = useRef(false);
  const itemsRef = useRef<Favorite[] | null>(null);
  itemsRef.current = items;
  // True from the lift until just after the drop: the click that ends a long press is not a tap.
  const justDragged = useRef(false);
  const releaseClick = () => { window.setTimeout(() => { justDragged.current = false; }, 250); };
  const notify = useRef(onCountChange); notify.current = onCountChange;

  /** Resolves true when the list on screen is the server's current one. */
  const load = useCallback(async (): Promise<boolean> => {
    // A drag or a save in flight must not be overwritten: reload afterwards instead.
    if (busy.current) { stale.current = true; return false; }
    const mine = ++version.current;
    try {
      const next = await api<FavoritesResponse>("/api/favorites");
      if (mine !== version.current || busy.current) { if (busy.current) stale.current = true; return false; }
      if (!Array.isArray(next?.favorites)) throw new Error("Unexpected favorites response");
      setItems(next.favorites); setError("");
      return true;
    } catch (e) {
      if (mine === version.current) setError(`Could not load favorites. ${(e as Error).message}`);
      return false;
    }
  }, []);
  const settle = useCallback(() => {
    busy.current = false; setSaving(false);
    if (stale.current) { stale.current = false; void load(); }
  }, [load]);

  useEffect(() => { void load(); }, [load, reloadKey]);
  const wasUpdating = useRef(updating);
  useEffect(() => { if (wasUpdating.current && !updating) void load(); wasUpdating.current = updating; }, [updating, load]);
  useEffect(() => { if (items) notify.current?.(items.length); }, [items]);

  const sensors = useSensors(
    useSensor(MouseSensor, { activationConstraint: ACTIVATION }),
    useSensor(TouchSensor, { activationConstraint: ACTIVATION }),
    // Enter opens the tile's page, so only Space lifts a tile (Space or Enter drops it).
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates, keyboardCodes: { start: ["Space"], cancel: ["Escape"], end: ["Space", "Enter"] } }),
  );

  const name = (id: unknown) => itemsRef.current?.find(f => f.instrumentId === id)?.name ?? "favorite";
  const place = (id: unknown) => `${(itemsRef.current?.findIndex(f => f.instrumentId === id) ?? 0) + 1} of ${itemsRef.current?.length ?? 0}`;
  const positionOf = (id: unknown) => itemsRef.current?.findIndex(f => f.instrumentId === id) ?? -1;
  const announcements: Announcements = {
    onDragStart: ({ active }) => `Picked up favorite ${name(active.id)}. Position ${place(active.id)}.`,
    // Over itself right after the lift: nothing moved yet, and a message here would replace "Picked up".
    onDragOver: ({ active, over }) => over && over.id !== active.id ? `Moved ${name(active.id)} to position ${positionOf(over.id) + 1} of ${itemsRef.current?.length ?? 0}.` : undefined,
    onDragEnd: ({ active, over }) => `Dropped ${name(active.id)} at position ${(over ? positionOf(over.id) : positionOf(active.id)) + 1} of ${itemsRef.current?.length ?? 0}.`,
    onDragCancel: () => "Reordering cancelled.",
  };

  const dragStart = ({ active: lifted }: DragStartEvent) => {
    justDragged.current = true;
    busy.current = true;
    setActive(itemsRef.current?.find(f => f.instrumentId === lifted.id) ?? null);
  };
  const dragEnd = async ({ active: lifted, over }: DragEndEvent) => {
    setActive(null); releaseClick();
    const before = itemsRef.current;
    const from = before?.findIndex(f => f.instrumentId === lifted.id) ?? -1;
    const to = over ? before?.findIndex(f => f.instrumentId === over.id) ?? -1 : -1;
    if (!before || from < 0 || to < 0 || from === to) { settle(); return; }
    const moved = arrayMove(before, from, to);
    const optimistic = reorderFavorites(before, favoriteIds(moved));
    setItems(optimistic); setSaving(true); setError(""); setNotice("");
    let reload = false;
    try {
      const saved = await api<FavoritesResponse | undefined>("/api/favorites/order", { method: "PUT", body: JSON.stringify({ instrumentIds: favoriteIds(optimistic) }) });
      if (Array.isArray(saved?.favorites)) setItems(saved.favorites);
    } catch (e) {
      // The rejected order never stays on screen: back to what was shown, then (on 409) to what the server has.
      setItems(before);
      if ((e as APIError).code === "FAVORITES_STALE") reload = true;
      else setError(`Could not save the new order. ${(e as Error).message}`);
    } finally { settle(); }
    if (reload) {
      stale.current = false;
      // A failed reload sets its own error; the notice only claims what happened.
      setNotice(await load() ? "Your favorites changed elsewhere. They were reloaded." : "");
    }
  };
  const dragCancel = () => { setActive(null); releaseClick(); settle(); };

  const asOf = items ? favoritesAsOf(items) : null;
  return <section className="favorites" data-testid="favorites" aria-labelledby="favorites-title">
    <div className="favorites-head">
      <h2 id="favorites-title">Favorites</h2>
      {asOf && <span className="favorites-asof muted" data-testid="favorites-asof">{asOfLabel(asOf)}</span>}
    </div>
    {error && <p className="error" role="alert">{error}</p>}
    {notice && <p className="muted" role="status" data-testid="favorites-notice">{notice}</p>}
    {items === null ? (!error && <p className="muted" role="status">Loading favorites…</p>)
      : items.length === 0 ? <p className="favorites-empty muted" data-testid="favorites-empty">No favorites yet. Star items under Manage items.</p>
      : <DndContext id="favorites-dnd" sensors={sensors} collisionDetection={closestCenter} accessibility={{ announcements, screenReaderInstructions: { draggable: REORDER_INSTRUCTIONS } }}
          onDragStart={dragStart} onDragEnd={e => void dragEnd(e)} onDragCancel={dragCancel}>
        <SortableContext items={favoriteIds(items)} strategy={rectSortingStrategy}>
          <ul className="favorites-grid" data-testid="favorites-list" aria-busy={saving || updating}>
            {items.map(favorite => <SortableTile key={favorite.instrumentId} favorite={favorite} disabled={saving} justDragged={justDragged} />)}
          </ul>
        </SortableContext>
        <DragOverlay>{active && <div className="favorite-tile tile is-lifted" aria-hidden="true"><TileBody favorite={active} /></div>}</DragOverlay>
      </DndContext>}
  </section>;
}
