"use client";

import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { api, APIError } from "../lib/api";
import {
  canImport,
  defaultSettings,
  displayImportField,
  editImportFields,
  fieldLabels,
  importSummary,
  rowPayload,
  same,
  type CSVSettings,
  type ImportFields,
  type ImportRow,
  type Preview,
  type SettingsResult,
} from "../lib/csv-import";
import { Modal } from "./portfolios";
import { usePortfolioUpdates } from "./portfolio-updates";
import { PageSize, TableCells, useTableColumns } from "./table-controls";

export function CSVImport({ id }: { id: string }) {
  const [pageSize, setPageSize] = useState(100);
  const columns = useTableColumns([
    "Include",
    "Row #",
    "Status",
    ...Object.values(fieldLabels),
    "Duplicate decision",
    "Actions",
  ]);
  const { changed, activeTab } = usePortfolioUpdates();

  const [stored, setStored] = useState<SettingsResult | null>(null);
  const [settings, setSettings] = useState(defaultSettings);
  const [file, setFile] = useState<File | null>(null);
  const [rows, setRows] = useState<ImportRow[]>([]);
  const [preview, setPreview] = useState<Preview | null>(null);
  const [headers, setHeaders] = useState<string[]>([]);
  const [pending, setPending] = useState(false);
  const [busy, setBusy] = useState(false);
  const [saving, setSaving] = useState(false);
  const [edited, setEdited] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const [confirm, setConfirm] = useState(false);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [filter, setFilter] = useState("All");
  const [page, setPage] = useState(1);
  const [cellText, setCellText] = useState<Record<string, string>>({});

  const original = useRef(new Map<number, ImportRow>());
  const excluded = useRef(new Set<number>());
  const generation = useRef(0);
  const loaded = useRef(false);
  const skipValidation = useRef(false);
  const fileInput = useRef<HTMLInputElement>(null);

  const validatedByNumber = new Map(preview?.rows.map((r) => [r.rowNumber, r]));
  const unsaved = !!stored && !same(settings, stored.settings);
  const dirty = edited || unsaved;

  useEffect(() => {
    if (activeTab !== "import" || loaded.current) return;
    let live = true;
    api<SettingsResult>("/api/users/me/csv-settings")
      .then((v) => {
        if (live) {
          loaded.current = true;
          setStored(v);
          setSettings(v.settings);
        }
      })
      .catch((e) => {
        if (live) setError(e.message);
      });
    return () => {
      live = false;
    };
  }, [activeTab]);

  useEffect(() => {
    if (!dirty || activeTab !== "import") return;
    const before = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = "";
    };
    const click = (e: MouseEvent) => {
      const anchor = (e.target as Element)?.closest?.(
        "a[href]",
      ) as HTMLAnchorElement | null;
      if (
        !anchor ||
        anchor.target === "_blank" ||
        e.ctrlKey ||
        e.metaKey ||
        e.shiftKey ||
        anchor.href === window.location.href
      )
        return;
      if (!window.confirm("Leave the Import tab with unsaved changes?")) {
        e.preventDefault();
        e.stopPropagation();
      }
    };
    const currentURL = window.location.href;
    const currentState = window.history.state;
    const pop = (e: PopStateEvent) => {
      if (!window.confirm("Leave the Import tab with unsaved changes?")) {
        e.stopImmediatePropagation();
        window.history.pushState(currentState, "", currentURL);
      }
    };
    window.addEventListener("beforeunload", before);
    document.addEventListener("click", click, true);
    window.addEventListener("popstate", pop, true);
    return () => {
      window.removeEventListener("beforeunload", before);
      document.removeEventListener("click", click, true);
      window.removeEventListener("popstate", pop, true);
    };
  }, [dirty, activeTab]);

  useEffect(() => {
    if (!file || !stored) return;
    const controller = new AbortController();
    const version = ++generation.current;
    setPending(true);
    setError("");
    const timer = setTimeout(async () => {
      try {
        const form = new FormData();
        form.append("file", file);
        form.append("settings", JSON.stringify(settings));
        const result = await api<Preview>(
          `/api/portfolios/${id}/imports/parse`,
          { method: "POST", body: form, signal: controller.signal },
        );
        if (version !== generation.current) return;
        original.current = new Map(
          result.rows.map((r) => [r.rowNumber, structuredClone(r)]),
        );
        excluded.current.clear();
        skipValidation.current = true;
        setCellText({});
        setHeaders(result.headers ?? []);
        setRows(result.rows);
        setPreview(result);
        setEdited(false);
        setPage(1);
      } catch (e) {
        if (!controller.signal.aborted && version === generation.current) {
          setError((e as Error).message);
          setPreview(null);
          setRows([]);
          setFieldErrors((e as APIError).details?.fields ?? {});
        }
      } finally {
        if (version === generation.current) setPending(false);
      }
    }, 300);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [file, settings, id, stored === null]);

  useEffect(() => {
    if (!rows.length) return;
    if (skipValidation.current) {
      skipValidation.current = false;
      return;
    }
    const controller = new AbortController();
    const version = ++generation.current;
    setPending(true);
    const timer = setTimeout(async () => {
      try {
        const result = await api<Preview>(
          `/api/portfolios/${id}/imports/validate`,
          {
            method: "POST",
            body: JSON.stringify({ rows: rowPayload(rows) }),
            signal: controller.signal,
          },
        );
        if (version !== generation.current) return;
        const fixed = result.rows.filter(
          (r) =>
            r.status !== "ERROR" &&
            !r.include &&
            !excluded.current.has(r.rowNumber),
        );
        if (fixed.length) {
          generation.current++;
          setRows((current) =>
            current.map((r) =>
              fixed.some((f) => f.rowNumber === r.rowNumber)
                ? { ...r, include: true }
                : r,
            ),
          );
          return;
        }
        setPreview(result);
      } catch (e) {
        if (!controller.signal.aborted && version === generation.current) {
          setError((e as Error).message);
          setPreview(null);
        }
      } finally {
        if (version === generation.current) setPending(false);
      }
    }, 500);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [rows, id]);

  const allowReparse = () =>
    !edited || window.confirm("Re-parsing discards your edits. Continue?");

  const configure = (next: CSVSettings) => {
    if (!allowReparse()) return;
    if (settings.headerRow && !next.headerRow) {
      const columns = headers.length
        ? headers
        : [
            "Symbol",
            "ISIN",
            "WKN",
            "Name",
            "Amount",
            "Open Price",
            "Date",
            "Total Invest",
          ];
      next = {
        ...next,
        mapping: Object.fromEntries(
          Object.entries(next.mapping).map(([key, column]) => {
            if (!column || !("header" in column)) return [key, column];
            const index = columns.findIndex(
              (h) =>
                h.trim().toLowerCase() === column.header.trim().toLowerCase(),
            );
            return [key, index < 0 ? null : { index: index + 1 }];
          }),
        ),
      };
    }
    generation.current++;
    setSettings(next);
    setFieldErrors({});
    setConfirm(false);
    if (file) {
      setRows([]);
      setPreview(null);
      setPending(true);
    }
    setEdited(false);
  };

  const mutate = (fn: (rows: ImportRow[]) => ImportRow[]) => {
    generation.current++;
    setPending(true);
    setError("");
    setConfirm(false);
    setEdited(true);
    setRows(fn);
  };

  const save = async () => {
    setSaving(true);
    setError("");
    setFieldErrors({});
    try {
      const v = await api<SettingsResult>("/api/users/me/csv-settings", {
        method: "PUT",
        body: JSON.stringify({ settings }),
      });
      setStored(v);
    } catch (e) {
      setError((e as Error).message);
      setFieldErrors((e as APIError).details?.fields ?? {});
    } finally {
      setSaving(false);
    }
  };

  const commit = async () => {
    if (!preview) return;
    setBusy(true);
    setError("");
    try {
      const v = await api<{
        linesCreated: number;
        transactionsCreated: number;
      }>(`/api/portfolios/${id}/imports`, {
        method: "POST",
        body: JSON.stringify({
          rows: rowPayload(preview.rows),
          settings,
          revision: preview.revision,
        }),
      });
      setSuccess(
        `Imported ${v.linesCreated} rows into ${v.transactionsCreated} transactions`,
      );
      setRows([]);
      setPreview(null);
      setEdited(false);
      setConfirm(false);
      setFile(null);
      if (fileInput.current) fileInput.current.value = "";
      original.current.clear();
      changed();
      const saved = await api<SettingsResult>("/api/users/me/csv-settings");
      setStored(saved);
      setSettings(saved.settings);
    } catch (e) {
      const err = e as APIError;
      setError(err.message);
      const fresh = (err.details as { preview?: Preview })?.preview;
      if (fresh) {
        skipValidation.current = true;
        setRows(fresh.rows);
        setPreview(fresh);
      }
      setConfirm(false);
    } finally {
      setBusy(false);
    }
  };

  const select = (
    label: string,
    key: keyof CSVSettings,
    options: [string, string][],
  ) => (
    <label key={key}>
      {label}
      <select
        aria-label={label}
        value={String(settings[key])}
        onChange={(e) => configure({ ...settings, [key]: e.target.value })}
      >
        {options.map(([v, l]) => (
          <option key={v} value={v}>
            {l}
          </option>
        ))}
      </select>
      {fieldErrors[key] && <span className="error">{fieldErrors[key]}</span>}
    </label>
  );

  const shown = rows
    .map((r) => {
      const validated = validatedByNumber.get(r.rowNumber);
      return {
        ...r,
        status: validated?.status ?? r.status,
        errors: validated?.errors ?? r.errors,
        warnings: validated?.warnings ?? r.warnings,
        instrument: validated?.instrument,
      };
    })
    .filter(
      (r) =>
        filter === "All" ||
        (filter === "Errors" && r.status === "ERROR") ||
        (filter === "Warnings" && r.warnings.length > 0) ||
        (filter === "Duplicates" && r.status === "DUPLICATE") ||
        (filter === "New instruments" &&
          !r.instrument &&
          r.status !== "ERROR") ||
        (filter === "Edited" &&
          !same(r.fields, original.current.get(r.rowNumber)?.fields)),
    );
  const pages = Math.max(1, Math.ceil(shown.length / pageSize));
  const currentPage = Math.min(page, pages);
  const s = preview?.summary;

  const importBar = (
    <>
      <div className="csv-actions csv-import-cta">
        {pending && (
          <span role="status" className="muted">
            Validating preview…
          </span>
        )}
        {s && (
          <div className="csv-summary" aria-live="polite">
            <span>
              {s.rows} rows · {s.included} included · {s.errors} errors ·{" "}
              {s.undecidedDuplicates} undecided duplicates
            </span>
            <span>
              {s.newInstruments} new instruments · {s.newItems} new portfolio
              items · {s.transactions} transactions to create
            </span>
            <strong>
              Portfolio items after import: {s.itemsAfter} / {s.itemLimit}
            </strong>
          </div>
        )}
        <button
          className="primary"
          disabled={busy || !canImport(preview, pending) || !rows.length}
          onClick={() => setConfirm(true)}
        >
          Import
        </button>
      </div>
    </>
  );

  return (
    <section className="csv-import">
      <h2>Import CSV</h2>
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
      {success && (
        <p role="status" className="toast">
          {success}. <Link href={`/portfolios/${id}/dashboard`}>Dashboard</Link>
        </p>
      )}
      {!stored ? (
        <p>Loading import configuration…</p>
      ) : (
        <>
          <div className="csv-file-picker">
            <label htmlFor={`csv-file-${id}`}>File</label>
            <div className="csv-file-control">
              <input
                id={`csv-file-${id}`}
                ref={fileInput}
                aria-label="CSV file"
                type="file"
                accept=".csv,.txt"
                hidden
                disabled={busy || saving}
                onChange={(e) => {
                  const next = e.target.files?.[0] ?? null;
                  if (!allowReparse()) {
                    e.target.value = "";
                    return;
                  }
                  generation.current++;
                  setFile(null);
                  setPending(false);
                  setPreview(null);
                  setRows([]);
                  setEdited(false);
                  setSuccess("");
                  if (next && next.size > 1048576) {
                    setError("File must be at most 1 MB.");
                    return;
                  }
                  setError("");
                  setFile(next);
                }}
              />
              <button
                type="button"
                className="primary csv-file-button"
                disabled={busy || saving}
                onClick={() => fileInput.current?.click()}
              >
                <svg
                  width="18"
                  height="18"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.75"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  aria-hidden="true"
                >
                  <path d="M12 16V4m-4 4 4-4 4 4M4 16v4h16v-4" />
                </svg>
                Choose file
              </button>
              {file && (
                <span className="csv-file-name">
                  {file.name} · {file.size.toLocaleString("de-DE")} bytes
                </span>
              )}
            </div>
          </div>
          <details className="csv-configuration">
            <summary>Configuration</summary>
            <fieldset disabled={busy || saving} aria-label="CSV configuration">
              <div className="csv-config">
                {select("Encoding", "encoding", [
                  ["auto", "Auto"],
                  ["utf-8", "UTF-8"],
                  ["windows-1252", "Windows-1252"],
                ])}
                <label>
                  Delimiter
                  <input
                    aria-label="Delimiter"
                    value={
                      settings.delimiter === "\t" ? "\\t" : settings.delimiter
                    }
                    placeholder="; , | or \\t"
                    onChange={(e) =>
                      configure({
                        ...settings,
                        delimiter:
                          e.target.value === "\\t" ? "\t" : e.target.value,
                      })
                    }
                  />
                  {fieldErrors.delimiter && (
                    <span className="error">{fieldErrors.delimiter}</span>
                  )}
                </label>
                {select("Quote character", "quoteChar", [
                  ['"', '"'],
                  ["'", "'"],
                  ["", "None"],
                ])}
                <label>
                  Header row
                  <select
                    aria-label="Header row"
                    value={String(settings.headerRow)}
                    onChange={(e) =>
                      configure({
                        ...settings,
                        headerRow: e.target.value === "true",
                      })
                    }
                  >
                    <option value="true">Yes</option>
                    <option value="false">No</option>
                  </select>
                </label>
                <label>
                  Skip leading lines
                  <input
                    aria-label="Skip leading lines"
                    type="number"
                    min="0"
                    max="20"
                    value={settings.skipLines}
                    onChange={(e) =>
                      configure({
                        ...settings,
                        skipLines: Number(e.target.value),
                      })
                    }
                  />
                </label>
                {select("Decimal separator", "decimalSeparator", [
                  [",", ","],
                  [".", "."],
                ])}
                {select("Thousands separator", "thousandsSeparator", [
                  [".", "."],
                  [",", ","],
                  [" ", "Space"],
                  ["'", "'"],
                  ["", "None"],
                ])}
                {select(
                  "Date format",
                  "dateFormat",
                  ["DD.MM.YYYY", "YYYY-MM-DD", "DD/MM/YYYY", "MM/DD/YYYY"].map(
                    (f) => [f, f],
                  ),
                )}
                <label>
                  Strip € / EUR
                  <select
                    aria-label="Strip € / EUR"
                    value={String(settings.stripEuro)}
                    onChange={(e) =>
                      configure({
                        ...settings,
                        stripEuro: e.target.value === "true",
                      })
                    }
                  >
                    <option value="true">Yes</option>
                    <option value="false">No</option>
                  </select>
                </label>
              </div>
              <h3>Column mapping</h3>
              <div className="csv-config">
                {Object.keys(settings.mapping).map((key) => {
                  const column = settings.mapping[key];
                  const value = column
                    ? "header" in column
                      ? "h:" + column.header
                      : "i:" + column.index
                    : "";
                  return (
                    <label key={key}>
                      {fieldLabels[key as keyof ImportFields]}
                      <select
                        aria-label={`Map ${key}`}
                        value={value}
                        onChange={(e) => {
                          const v = e.target.value;
                          configure({
                            ...settings,
                            mapping: {
                              ...settings.mapping,
                              [key]: !v
                                ? null
                                : v.startsWith("h:")
                                  ? { header: v.slice(2) }
                                  : { index: Number(v.slice(2)) },
                            },
                          });
                        }}
                      >
                        <option value="">Not mapped</option>
                        {settings.headerRow ? (
                          <>
                            {column &&
                              "header" in column &&
                              !headers.includes(column.header) && (
                                <option value={value}>{column.header}</option>
                              )}
                            {headers.map((h, i) => (
                              <option key={i} value={`h:${h}`}>
                                {h || `Column ${i + 1}`}
                              </option>
                            ))}
                          </>
                        ) : (
                          <>
                            {Array.from(
                              {
                                length: Math.max(
                                  8,
                                  ...rows.map((r) => r.raw?.length ?? 0),
                                  column && "index" in column
                                    ? column.index
                                    : 0,
                                ),
                              },
                              (_, i) => (
                                <option key={i} value={`i:${i + 1}`}>
                                  Column {i + 1}
                                </option>
                              ),
                            )}
                          </>
                        )}
                        {settings.headerRow && column && "index" in column && (
                          <option value={value}>Column {column.index}</option>
                        )}
                      </select>
                      {fieldErrors[`mapping.${key}`] && (
                        <span className="error">
                          {fieldErrors[`mapping.${key}`]}
                        </span>
                      )}
                    </label>
                  );
                })}
              </div>
              <div className="csv-actions">
                <button
                  disabled={!unsaved || saving}
                  onClick={() => void save()}
                >
                  Save configuration
                </button>
                <button onClick={() => configure(defaultSettings())}>
                  Reset to defaults
                </button>
                <button
                  disabled={!unsaved}
                  onClick={() => configure(structuredClone(stored.settings))}
                >
                  Discard changes
                </button>
                {unsaved && <span role="status">Unsaved changes</span>}
                {stored.updatedAt && (
                  <span className="muted">
                    Saved{" "}
                    {new Date(stored.updatedAt).toLocaleString("de-DE", {
                      day: "2-digit",
                      month: "2-digit",
                      year: "numeric",
                      hour: "2-digit",
                      minute: "2-digit",
                    })}
                  </span>
                )}
              </div>
            </fieldset>
          </details>
          {(pending || !!preview || !!rows.length) && importBar}
          {!!rows.length && (
            <>
              <div className="csv-actions">
                <label>
                  Filter
                  <select
                    aria-label="Filter"
                    value={filter}
                    onChange={(e) => {
                      setFilter(e.target.value);
                      setPage(1);
                    }}
                  >
                    {[
                      "All",
                      "Errors",
                      "Warnings",
                      "Duplicates",
                      "New instruments",
                      "Edited",
                    ].map((f) => (
                      <option key={f}>{f}</option>
                    ))}
                  </select>
                </label>
                {["skip", "import"].map((decision) => (
                  <button
                    key={decision}
                    disabled={busy}
                    onClick={() =>
                      mutate((rs) =>
                        rs.map((r) => ({
                          ...r,
                          decision:
                            validatedByNumber.get(r.rowNumber)?.status ===
                            "DUPLICATE"
                              ? decision
                              : r.decision,
                        })),
                      )
                    }
                  >
                    {decision === "skip" ? "Skip" : "Import"} all duplicates
                  </button>
                ))}
                <button
                  disabled={busy}
                  onClick={() =>
                    mutate((rs) =>
                      rs.map((r) => {
                        if (
                          validatedByNumber.get(r.rowNumber)?.status !== "ERROR"
                        )
                          return r;
                        excluded.current.add(r.rowNumber);
                        return { ...r, include: false };
                      }),
                    )
                  }
                >
                  Exclude rows with errors
                </button>
              </div>
              <div className="table-toolbar">
                <PageSize
                  value={pageSize}
                  onChange={(value) => {
                    setPageSize(value);
                    setPage(1);
                  }}
                />
                {columns.controls}
              </div>
              <div className="table-scroll">
                <table className="csv-table">
                  <thead>
                    <tr>{columns.headers}</tr>
                  </thead>
                  <tbody>
                    {shown
                      .slice(
                        (currentPage - 1) * pageSize,
                        currentPage * pageSize,
                      )
                      .map((row) => (
                        <tr key={row.rowNumber}>
                          <TableCells visible={columns.visible} labels={columns.labels}>
                            <td>
                              {/* The label is the 44px hit area; the input keeps its accessible name. */}
                              <label className="csv-include">
                                <input
                                  aria-label={`Include row ${row.rowNumber}`}
                                  type="checkbox"
                                  checked={row.include}
                                  disabled={busy}
                                  onChange={(e) => {
                                    if (e.target.checked)
                                      excluded.current.delete(row.rowNumber);
                                    else excluded.current.add(row.rowNumber);
                                    mutate((rs) =>
                                      rs.map((r) =>
                                        r.rowNumber === row.rowNumber
                                          ? { ...r, include: e.target.checked }
                                          : r,
                                      ),
                                    );
                                  }}
                                />
                              </label>
                            </td>
                            <td>{row.rowNumber}</td>
                            <td>
                              <span className="badge">{row.status}</span>
                              {row.warnings.map((w) => (
                                <p className="hint" key={w}>
                                  {w}
                                </p>
                              ))}
                            </td>
                            {(
                              Object.keys(fieldLabels) as (keyof ImportFields)[]
                            ).map((field) => {
                              const metadata = [
                                "ticker",
                                "name",
                                "type",
                              ].includes(field);
                              const locked = metadata && !!row.instrument;
                              const value = locked
                                ? String(
                                    row.instrument?.[
                                      field as "ticker" | "name" | "type"
                                    ] ?? "",
                                  )
                                : (cellText[`${row.rowNumber}:${field}`] ??
                                  displayImportField(field, row.fields[field]));
                              const editedCell = !same(
                                row.fields[field],
                                original.current.get(row.rowNumber)?.fields[
                                  field
                                ],
                              );
                              return (
                                <td
                                  key={field}
                                  className={editedCell ? "csv-edited" : ""}
                                >
                                  {locked ? (
                                    <span>{value}</span>
                                  ) : field === "type" ? (
                                    <select
                                      aria-label={`Row ${row.rowNumber} ${fieldLabels[field]}`}
                                      value={row.fields.type}
                                      disabled={busy}
                                      onChange={(e) =>
                                        mutate((rs) =>
                                          rs.map((r) =>
                                            r.rowNumber === row.rowNumber
                                              ? {
                                                  ...r,
                                                  fields: {
                                                    ...r.fields,
                                                    type: e.target.value,
                                                  },
                                                }
                                              : r,
                                          ),
                                        )
                                      }
                                    >
                                      <option value="">Auto</option>
                                      <option>ETF</option>
                                      <option>STOCK</option>
                                    </select>
                                  ) : (
                                    <input
                                      aria-label={`Row ${row.rowNumber} ${fieldLabels[field]}`}
                                      value={value}
                                      disabled={busy}
                                      aria-invalid={!!row.errors[field]}
                                      onChange={(e) => {
                                        const raw = e.target.value;
                                        setCellText((current) => {
                                          const next = { ...current };
                                          for (const key of [
                                            "quantity",
                                            "price",
                                            "total",
                                          ])
                                            delete next[
                                              `${row.rowNumber}:${key}`
                                            ];
                                          next[`${row.rowNumber}:${field}`] =
                                            raw;
                                          return next;
                                        });
                                        mutate((rs) =>
                                          rs.map((r) =>
                                            r.rowNumber === row.rowNumber
                                              ? {
                                                  ...r,
                                                  fields: editImportFields(
                                                    r.fields,
                                                    field,
                                                    raw,
                                                  ),
                                                }
                                              : r,
                                          ),
                                        );
                                      }}
                                    />
                                  )}{" "}
                                  {row.errors[field] && (
                                    <p className="error">{row.errors[field]}</p>
                                  )}
                                </td>
                              );
                            })}
                            <td>
                              {row.status === "DUPLICATE" && (
                                <select
                                  aria-label={`Row ${row.rowNumber} duplicate decision`}
                                  value={row.decision ?? ""}
                                  disabled={busy}
                                  onChange={(e) =>
                                    mutate((rs) =>
                                      rs.map((r) =>
                                        r.rowNumber === row.rowNumber
                                          ? { ...r, decision: e.target.value }
                                          : r,
                                      ),
                                    )
                                  }
                                >
                                  <option value="">Choose…</option>
                                  <option value="skip">Skip</option>
                                  <option value="import">Import</option>
                                </select>
                              )}
                            </td>
                            <td>
                              <button
                                disabled={busy}
                                aria-label={`Delete row ${row.rowNumber}`}
                                onClick={() => {
                                  mutate((rs) =>
                                    rs.filter(
                                      (r) => r.rowNumber !== row.rowNumber,
                                    ),
                                  );
                                  if (rows.length === 1) {
                                    setPreview(null);
                                    setPending(false);
                                  }
                                }}
                              >
                                ×
                              </button>
                              <button
                                disabled={
                                  busy ||
                                  same(
                                    row.fields,
                                    original.current.get(row.rowNumber)?.fields,
                                  )
                                }
                                onClick={() => {
                                  setCellText((current) =>
                                    Object.fromEntries(
                                      Object.entries(current).filter(
                                        ([key]) =>
                                          !key.startsWith(`${row.rowNumber}:`),
                                      ),
                                    ),
                                  );
                                  mutate((rs) =>
                                    rs.map((r) =>
                                      r.rowNumber === row.rowNumber
                                        ? {
                                            ...structuredClone(
                                              original.current.get(
                                                r.rowNumber,
                                              )!,
                                            ),
                                            include: r.include,
                                            decision: undefined,
                                          }
                                        : r,
                                    ),
                                  );
                                }}
                              >
                                Revert to file value
                              </button>
                            </td>
                          </TableCells>
                        </tr>
                      ))}
                  </tbody>
                </table>
              </div>
              <div className="pagination">
                <button
                  disabled={currentPage === 1}
                  onClick={() => setPage(currentPage - 1)}
                >
                  Previous
                </button>
                <span>
                  Page {currentPage} / {pages}
                </span>
                <button
                  disabled={currentPage === pages}
                  onClick={() => setPage(currentPage + 1)}
                >
                  Next
                </button>
              </div>
            </>
          )}
        </>
      )}
      {confirm && s && (
        <Modal
          title="Confirm import"
          close={() => {
            if (!busy) setConfirm(false);
          }}
        >
          <p>{importSummary(s)}</p>
          <div className="dialog-actions">
            <button disabled={busy} onClick={() => setConfirm(false)}>
              Cancel
            </button>
            <button
              className="primary"
              disabled={busy || pending}
              onClick={() => void commit()}
            >
              {busy ? "Importing…" : "Confirm import"}
            </button>
          </div>
        </Modal>
      )}
    </section>
  );
}
