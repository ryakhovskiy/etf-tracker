package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

var ErrNameTaken = errors.New("A portfolio with this name already exists.")
var ErrInvalid = errors.New("Invalid instrument ids.")

type Portfolio struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	ItemCount int    `json:"itemCount"`
	CreatedAt string `json:"createdAt"`
}
type PortfolioDetail struct {
	Portfolio
	InstrumentIDs []int64 `json:"instrumentIds"`
	ItemLimit     int     `json:"itemLimit"`
}
type Instrument struct {
	ID          int64   `json:"id"`
	Type        string  `json:"type"`
	Name        string  `json:"name"`
	Ticker      string  `json:"ticker"`
	ISIN        string  `json:"isin"`
	WKN         *string `json:"wkn"`
	InPortfolio bool    `json:"inPortfolio"`
}

func (s *Store) Portfolios(ctx context.Context, user string) ([]Portfolio, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,p.name,p.created_at,COUNT(i.instrument_id) FROM portfolios p LEFT JOIN portfolio_items i ON i.portfolio_id=p.id WHERE p.user_id=? GROUP BY p.id ORDER BY p.name`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Portfolio{}
	for rows.Next() {
		var p Portfolio
		if err := rows.Scan(&p.ID, &p.Name, &p.CreatedAt, &p.ItemCount); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}
func (s *Store) Portfolio(ctx context.Context, user string, id int64) (PortfolioDetail, error) {
	var p PortfolioDetail
	// One query provides a consistent ownership and membership snapshot.
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,p.name,p.created_at,i.instrument_id FROM portfolios p LEFT JOIN portfolio_items i ON i.portfolio_id=p.id WHERE p.id=? AND p.user_id=? ORDER BY i.instrument_id`, id, user)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	p.InstrumentIDs = []int64{}
	p.ItemLimit = s.limits.Items
	for rows.Next() {
		var item sql.NullInt64
		if err := rows.Scan(&p.ID, &p.Name, &p.CreatedAt, &item); err != nil {
			return p, err
		}
		if item.Valid {
			p.InstrumentIDs = append(p.InstrumentIDs, item.Int64)
		}
	}
	if err := rows.Err(); err != nil {
		return p, err
	}
	if p.ID == 0 {
		return p, ErrNotFound
	}
	p.ItemCount = len(p.InstrumentIDs)
	return p, nil
}
func owns(ctx context.Context, conn *sql.Conn, user string, id int64) error {
	var found int
	err := conn.QueryRowContext(ctx, "SELECT 1 FROM portfolios WHERE id=? AND user_id=?", id, user).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
func (s *Store) SavePortfolio(ctx context.Context, user string, id int64, name string) (Portfolio, error) {
	var p Portfolio
	err := s.write(ctx, func(conn *sql.Conn) error {
		if id != 0 {
			if err := owns(ctx, conn, user, id); err != nil {
				return err
			}
		}
		var duplicate int
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM portfolios WHERE user_id=? AND name=? AND id!=?", user, name, id).Scan(&duplicate); err != nil {
			return err
		}
		if duplicate > 0 {
			return ErrNameTaken
		}
		if id == 0 {
			var current int
			if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM portfolios WHERE user_id=?", user).Scan(&current); err != nil {
				return err
			}
			if current >= s.limits.Portfolios {
				return limitError("PORTFOLIO_LIMIT_REACHED", s.limits.Portfolios, current)
			}
			result, err := conn.ExecContext(ctx, "INSERT INTO portfolios(user_id,name) VALUES (?,?)", user, name)
			if err != nil {
				return err
			}
			id, err = result.LastInsertId()
			if err != nil {
				return err
			}
		} else {
			if _, err := conn.ExecContext(ctx, "UPDATE portfolios SET name=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND user_id=?", name, id, user); err != nil {
				return err
			}
		}
		return conn.QueryRowContext(ctx, `SELECT id,name,created_at,(SELECT COUNT(*) FROM portfolio_items WHERE portfolio_id=p.id) FROM portfolios p WHERE id=? AND user_id=?`, id, user).Scan(&p.ID, &p.Name, &p.CreatedAt, &p.ItemCount)
	})
	return p, err
}
func (s *Store) DeletePortfolio(ctx context.Context, user string, id int64) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM portfolios WHERE id=? AND user_id=?", id, user)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

type AddedItems struct {
	Added   []int64 `json:"added"`
	Skipped []int64 `json:"skipped"`
}

func (s *Store) AddItems(ctx context.Context, user string, id int64, ids []int64) (AddedItems, error) {
	result := AddedItems{[]int64{}, []int64{}}
	err := s.write(ctx, func(conn *sql.Conn) error {
		if err := owns(ctx, conn, user, id); err != nil {
			return err
		}
		var current int
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM portfolio_items WHERE portfolio_id=?", id).Scan(&current); err != nil {
			return err
		}
		seen := map[int64]bool{}
		for _, item := range ids {
			if seen[item] {
				continue
			}
			seen[item] = true
			var exists, member int
			if err := conn.QueryRowContext(ctx, `SELECT COUNT(*),EXISTS(SELECT 1 FROM portfolio_items WHERE portfolio_id=? AND instrument_id=?) FROM instruments WHERE id=?`, id, item, item).Scan(&exists, &member); err != nil {
				return err
			}
			if exists == 0 {
				return ErrInvalid
			}
			if member == 1 {
				result.Skipped = append(result.Skipped, item)
			} else {
				result.Added = append(result.Added, item)
			}
		}
		if current+len(result.Added) > s.limits.Items {
			return limitError("ITEM_LIMIT_REACHED", s.limits.Items, current)
		}
		for _, item := range result.Added {
			if _, err := conn.ExecContext(ctx, "INSERT OR IGNORE INTO portfolio_items(portfolio_id,instrument_id) VALUES (?,?)", id, item); err != nil {
				return err
			}
		}
		return nil
	})
	return result, err
}
func (s *Store) RemoveItem(ctx context.Context, user string, id, item int64) error {
	return s.write(ctx, func(conn *sql.Conn) error {
		if err := owns(ctx, conn, user, id); err != nil {
			return err
		}
		var count int
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM transaction_lines l JOIN transactions t ON t.id=l.transaction_id WHERE t.portfolio_id=? AND l.instrument_id=?", id, item).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return ErrItemHasTransactions
		}
		res, err := conn.ExecContext(ctx, "DELETE FROM portfolio_items WHERE portfolio_id=? AND instrument_id=?", id, item)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}
func (s *Store) Search(ctx context.Context, user, q string, portfolio int64) ([]Instrument, error) {
	result := []Instrument{}
	// Ownership check and search share one snapshot, so no foreign membership leaks.
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN"); err != nil {
		return nil, err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	if portfolio != 0 {
		if err := owns(ctx, conn, user, portfolio); err != nil {
			return nil, err
		}
	}
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	rows, err := conn.QueryContext(ctx, `SELECT id,type,name,ticker,isin,wkn,EXISTS(SELECT 1 FROM portfolio_items WHERE portfolio_id=? AND instrument_id=i.id) FROM instruments i
 WHERE ticker LIKE ? ESCAPE '\' OR isin LIKE ? ESCAPE '\' OR wkn LIKE ? ESCAPE '\' OR name LIKE ? ESCAPE '\'
 ORDER BY CASE WHEN ticker=? COLLATE NOCASE OR isin=? COLLATE NOCASE OR wkn=? COLLATE NOCASE THEN 0
 WHEN ticker LIKE ? ESCAPE '\' OR isin LIKE ? ESCAPE '\' OR wkn LIKE ? ESCAPE '\' OR name LIKE ? ESCAPE '\' THEN 1 ELSE 2 END, name ASC LIMIT 10`, portfolio, "%"+escaped+"%", "%"+escaped+"%", "%"+escaped+"%", "%"+escaped+"%", q, q, q, escaped+"%", escaped+"%", escaped+"%", escaped+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var i Instrument
		if err := rows.Scan(&i.ID, &i.Type, &i.Name, &i.Ticker, &i.ISIN, &i.WKN, &i.InPortfolio); err != nil {
			return nil, err
		}
		result = append(result, i)
	}
	return result, rows.Err()
}
