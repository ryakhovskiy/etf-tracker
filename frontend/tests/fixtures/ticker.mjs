import { favoriteInstruments } from './favorites.mjs';

const iso = date => date.toISOString().slice(0, 10);
const monthsAgo = (day, months) => {
  const date = new Date(`${day}T00:00:00Z`);
  date.setUTCDate(1);
  date.setUTCMonth(date.getUTCMonth() - months);
  return iso(date);
};

// The endpoint already converts prices to EUR. Keep all money and units as
// decimal strings, including buys in separate portfolios on the same day.
export function tickerChart({ day = iso(new Date()), ...overrides } = {}) {
  const instrument = favoriteInstruments[0];
  const buyDay = monthsAgo(day, 2);
  return {
    instrument: { id: 1, name: instrument.name, isin: 'IE00B4X9L533', ticker: instrument.ticker },
    favorite: true,
    latest: { priceEur: '42.2900', change: { amount: '1.0880', percent: '2.64' }, asOf: { ...instrument.asOf, quoteDate: day, quoteTime: `${day}T10:21:47Z`, fetchedAt: `${day}T10:22:00Z` } },
    points: [
      { day: monthsAgo(day, 72), closeEur: '25.0000' },
      { day: monthsAgo(day, 18), closeEur: '30.0000' },
      { day: monthsAgo(day, 6), closeEur: '37.2000' },
      { day: buyDay, closeEur: '40.0000' },
      { day, closeEur: '42.2900' },
    ],
    buys: [
      { day: monthsAgo(day, 72), units: '1.000000', priceEur: '25.0000', portfolioId: 1, portfolioName: 'Retirement portfolio' },
      { day: buyDay, units: '2.500000', priceEur: '50.0000', portfolioId: 1, portfolioName: 'Retirement portfolio' },
      { day: buyDay, units: '0.750000', priceEur: '35.0000', portfolioId: 2, portfolioName: 'Family savings' },
      { day: buyDay, units: '1.000000', priceEur: '40.0000', portfolioId: 2, portfolioName: 'Family savings' },
    ],
    from: monthsAgo(day, 72),
    to: day,
    ...structuredClone(overrides),
  };
}
