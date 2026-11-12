import assert from 'node:assert/strict';
import { test } from 'node:test';
import { chartDay, chartPeriods, clampChartRange, monthsBefore, panChartRange, periodChartRange, zoomChartRange } from './chart-range.ts';

const at = date => Date.parse(`${date}T00:00:00Z`);

test('periods use calendar months and clamp month ends across leap years', () => {
 assert.equal(monthsBefore(at('2026-05-31'),3),at('2026-02-28'));
 assert.equal(monthsBefore(at('2024-05-31'),3),at('2024-02-29'));
 assert.equal(monthsBefore(at('2024-02-29'),12),at('2023-02-28'));
 const bounds = [at('2010-01-01'),at('2026-11-12')];
 assert.deepEqual(chartPeriods,[3,6,12,18,24,48,60]);
 for(const months of chartPeriods) assert.deepEqual(periodChartRange(bounds,months),[monthsBefore(bounds[1],months),bounds[1]]);
 assert.deepEqual(periodChartRange(bounds,'max'),bounds);
 const recent = [at('2026-10-01'),bounds[1]];
 assert.deepEqual(periodChartRange(recent,3),recent);
});

test('panning preserves the time span and stops at the first transaction and latest date', () => {
 const bounds = [0,100*chartDay], range = [20*chartDay,40*chartDay];
 assert.deepEqual(panChartRange(range,bounds,-30*chartDay),[0,20*chartDay]);
 assert.deepEqual(panChartRange(range,bounds,90*chartDay),[80*chartDay,100*chartDay]);
 assert.deepEqual(panChartRange(range,bounds,10*chartDay),[30*chartDay,50*chartDay]);
 assert.deepEqual(panChartRange(bounds,bounds,10*chartDay),bounds);
});

test('zoom preserves the cursor anchor, clamps to history, and never narrows below one day', () => {
 const bounds = [0,100*chartDay], range = [20*chartDay,60*chartDay];
 assert.deepEqual(zoomChartRange(range,bounds,0.5),[30*chartDay,50*chartDay]);
 assert.deepEqual(zoomChartRange(range,bounds,0.5,0.25),[25*chartDay,45*chartDay]);
 assert.deepEqual(zoomChartRange(range,bounds,0.5,1),[40*chartDay,60*chartDay]);
 assert.deepEqual(zoomChartRange(range,bounds,10),bounds);
 const smallest = zoomChartRange(range,bounds,0.00001);
 assert.equal(smallest[1]-smallest[0],chartDay);
 assert.deepEqual(zoomChartRange([0,1000],[0,1000],0.5),[0,1000]);
 assert.deepEqual(clampChartRange([0,200*chartDay],bounds),bounds);
});
