export type ChartRange = [number, number];
export const chartPeriods = [3, 6, 12, 18, 24, 48, 60] as const;
export type ChartPreset = typeof chartPeriods[number] | "max";
export type ChartPeriod = ChartPreset | "custom";
export const chartPeriodStorageKey = "holdings:chart-period";
export const chartDay = 86_400_000;

export function parseChartPreset(value: string | null): ChartPreset | null {
 return value === "max" ? "max" : chartPeriods.find(months => String(months) === value) ?? null;
}

/** Keep calendar-month presets on the same day, clamping month ends (e.g. May 31 → February 28). */
export function monthsBefore(timestamp: number, months: number) {
 const date = new Date(timestamp);
 const day = date.getUTCDate();
 date.setUTCDate(1);
 date.setUTCMonth(date.getUTCMonth() - months);
 const lastDay = new Date(Date.UTC(date.getUTCFullYear(), date.getUTCMonth() + 1, 0)).getUTCDate();
 date.setUTCDate(Math.min(day, lastDay));
 return date.getTime();
}

export function clampChartRange(range: ChartRange, bounds: ChartRange): ChartRange {
 const width = Math.min(Math.max(range[1] - range[0], Math.min(chartDay, bounds[1] - bounds[0])), bounds[1] - bounds[0]);
 const start = Math.max(bounds[0], Math.min(range[0], bounds[1] - width));
 return [start, start + width];
}

export function periodChartRange(bounds: ChartRange, period: ChartPeriod): ChartRange {
 return typeof period === "number" ? clampChartRange([monthsBefore(bounds[1], period), bounds[1]], bounds) : [...bounds];
}

export function panChartRange(range: ChartRange, bounds: ChartRange, offset: number): ChartRange {
 return clampChartRange([range[0] + offset, range[1] + offset], bounds);
}

export function zoomChartRange(range: ChartRange, bounds: ChartRange, factor: number, anchor = 0.5): ChartRange {
 const fraction = Math.max(0, Math.min(1, anchor));
 const width = Math.min(bounds[1] - bounds[0], Math.max(Math.min(chartDay, bounds[1] - bounds[0]), (range[1] - range[0]) * factor));
 const start = range[0] + (range[1] - range[0] - width) * fraction;
 return clampChartRange([start, start + width], bounds);
}
