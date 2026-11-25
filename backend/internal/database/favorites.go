package database

import (
	"context"
	"database/sql"
	"errors"
	"math/big"

	"holdings/backend/internal/marketdata"
)

// ErrFavoritesStale means a reorder named a different set of favorites than the
// user has now (starred or unstarred elsewhere in the meantime).
var ErrFavoritesStale = errors.New("The favorites changed. Reload and try again.")

// FavoriteChange is the EUR change of a title against its previous close.
type FavoriteChange struct {
	Amount  string  `json:"amount"`
	Percent *string `json:"percent"`
}

// Favorite is one starred title with its latest stored quote (no provider call).
// PriceEUR, Change and AsOf are null when the data is missing; Reasons says why
// (ReasonNoQuote, ReasonNoPrevClose, ReasonFXPrevMissing). A previous close that
// can only be converted at the quote's own FX rate gives no change.
type Favorite struct {
	InstrumentID int64           `json:"instrumentId"`
	Name         string          `json:"name"`
	Ticker       string          `json:"ticker"`
	Position     int             `json:"position"`
	PriceEUR     *string         `json:"priceEur"`
	Change       *FavoriteChange `json:"change"`
	AsOf         *SummaryAsOf    `json:"asOf"`
	Reasons      []string        `json:"reasons"`
}

// favoriteFigures values one title from its latest quote with the same quote,
// FX and previous-close rules as the summaries (a single position's change).
func favoriteFigures(f *Favorite, q *marketdata.Quote) {
	f.Reasons = []string{}
	if q == nil {
		f.Reasons = append(f.Reasons, ReasonNoQuote)
		return
	}
	price := q.Price
	f.PriceEUR = &price
	// Reuse the summary's as-of description; result() only fills it once a quote
	// counted as used (hasQuote), so mark it explicitly.
	acc := newSummaryAcc()
	acc.noteQuote(q)
	acc.hasQuote = true
	f.AsOf = acc.result().AsOf
	cur, ok := new(big.Rat).SetString(q.Price)
	if !ok {
		f.PriceEUR, f.AsOf = nil, nil
		f.Reasons = append(f.Reasons, ReasonNoQuote)
		return
	}
	prev, ok := previousClose(q)
	if !ok {
		f.Reasons = append(f.Reasons, ReasonNoPrevClose)
		return
	}
	if prev.approximate {
		// Unlike a summary, a tile carries no "partial" marker, so an amount
		// converted at today's FX rate is withheld rather than shown as exact.
		f.Reasons = append(f.Reasons, ReasonFXPrevMissing)
		return
	}
	delta := new(big.Rat).Sub(cur, prev.eur)
	f.Change = &FavoriteChange{Amount: money(delta), Percent: pct(delta, prev.eur)}
}

// Favorites lists the user's starred titles in order.
func (s *Store) Favorites(ctx context.Context, user string) ([]Favorite, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.instrument_id,i.name,i.ticker,f.position
		FROM favorites f JOIN instruments i ON i.id=f.instrument_id
		WHERE f.user_id=? ORDER BY f.position,f.instrument_id`, user)
	if err != nil {
		return nil, err
	}
	out := []Favorite{}
	var ids []int64
	for rows.Next() {
		var f Favorite
		if err = rows.Scan(&f.InstrumentID, &f.Name, &f.Ticker, &f.Position); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, f)
		ids = append(ids, f.InstrumentID)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	quotes, err := s.latestQuotes(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		favoriteFigures(&out[i], quotes[out[i].InstrumentID])
	}
	return out, nil
}

// StarFavorite stars an instrument held in one of the user's portfolios and
// appends it at the end. Idempotent: an already starred title keeps its place.
// Any other instrument is ErrNotFound. It returns the title's position.
func (s *Store) StarFavorite(ctx context.Context, user string, instrumentID int64) (int, error) {
	position := 0
	err := s.write(ctx, func(c *sql.Conn) error {
		var member bool
		if err := c.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM portfolio_items pi JOIN portfolios p ON p.id=pi.portfolio_id WHERE p.user_id=? AND pi.instrument_id=?)`, user, instrumentID).Scan(&member); err != nil {
			return err
		}
		if !member {
			return ErrNotFound
		}
		err := c.QueryRowContext(ctx, "SELECT position FROM favorites WHERE user_id=? AND instrument_id=?", user, instrumentID).Scan(&position)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err = c.QueryRowContext(ctx, "SELECT COUNT(*) FROM favorites WHERE user_id=?", user).Scan(&position); err != nil {
			return err
		}
		_, err = c.ExecContext(ctx, "INSERT INTO favorites(user_id,instrument_id,position) VALUES (?,?,?)", user, instrumentID, position)
		return err
	})
	return position, err
}

// UnstarFavorite removes a favorite and compacts the positions. A title the user
// has not starred is ErrNotFound.
func (s *Store) UnstarFavorite(ctx context.Context, user string, instrumentID int64) error {
	return s.write(ctx, func(c *sql.Conn) error {
		res, err := c.ExecContext(ctx, "DELETE FROM favorites WHERE user_id=? AND instrument_id=?", user, instrumentID)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return ErrNotFound
		}
		return compactFavorites(ctx, c, user)
	})
}

// ReorderFavorites rewrites the positions to follow ids, atomically. ids must
// name exactly the user's current favorites (each once), else ErrFavoritesStale
// and nothing changes.
func (s *Store) ReorderFavorites(ctx context.Context, user string, ids []int64) error {
	return s.write(ctx, func(c *sql.Conn) error {
		current, err := favoriteIDs(ctx, c, user)
		if err != nil {
			return err
		}
		if len(current) != len(ids) {
			return ErrFavoritesStale
		}
		known := make(map[int64]bool, len(current))
		for _, id := range current {
			known[id] = true
		}
		for _, id := range ids {
			if !known[id] {
				return ErrFavoritesStale
			}
			delete(known, id) // a repeated id is missing the second time
		}
		for i, id := range ids {
			if _, err := c.ExecContext(ctx, "UPDATE favorites SET position=? WHERE user_id=? AND instrument_id=?", i, user, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// favoriteIDs returns the user's favorite instrument ids in position order.
func favoriteIDs(ctx context.Context, c *sql.Conn, user string) ([]int64, error) {
	rows, err := c.QueryContext(ctx, "SELECT instrument_id FROM favorites WHERE user_id=? ORDER BY position,instrument_id", user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// compactFavorites renumbers the user's positions 0..n-1 keeping their order.
func compactFavorites(ctx context.Context, c *sql.Conn, user string) error {
	ids, err := favoriteIDs(ctx, c, user)
	if err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := c.ExecContext(ctx, "UPDATE favorites SET position=? WHERE user_id=? AND instrument_id=? AND position!=?", i, user, id, i); err != nil {
			return err
		}
	}
	return nil
}

// pruneFavorites drops the user's favorites that no portfolio of theirs holds any
// more and compacts the positions. Callers run it inside the write transaction
// that removed the item or the portfolio.
func pruneFavorites(ctx context.Context, c *sql.Conn, user string) error {
	res, err := c.ExecContext(ctx, `DELETE FROM favorites WHERE user_id=? AND instrument_id NOT IN
		(SELECT pi.instrument_id FROM portfolio_items pi JOIN portfolios p ON p.id=pi.portfolio_id WHERE p.user_id=?)`, user, user)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return err
	}
	return compactFavorites(ctx, c, user)
}
