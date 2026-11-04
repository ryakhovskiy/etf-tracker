package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
)

const MaxUsers = 10
const MaxPortfoliosPerUser = 10
const MaxItemsPerPortfolio = 30

type Limits struct{ Users, Portfolios, Items int }
type LimitError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Limit   int    `json:"limit"`
	Current int    `json:"current"`
}

func (e *LimitError) Error() string { return e.Message }
func limitError(code string, limit, current int) error {
	messages := map[string]string{
		"USER_LIMIT_REACHED":      "Signups are currently closed.",
		"PORTFOLIO_LIMIT_REACHED": fmt.Sprintf("Portfolio limit reached (%d / %d). Delete a portfolio to create another.", current, limit),
		"ITEM_LIMIT_REACHED":      fmt.Sprintf("This batch exceeds the item limit (%d / %d items). Only %d more items fit.", current, limit, max(0, limit-current)),
	}
	return &LimitError{code, messages[code], limit, current}
}
func loadLimits() (Limits, error) {
	l := Limits{MaxUsers, MaxPortfoliosPerUser, MaxItemsPerPortfolio}
	for key, dest := range map[string]*int{"MAX_USERS": &l.Users, "MAX_PORTFOLIOS_PER_USER": &l.Portfolios, "MAX_ITEMS_PER_PORTFOLIO": &l.Items} {
		if v, ok := os.LookupEnv(key); ok {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return l, fmt.Errorf("%s must be a positive integer", key)
			}
			*dest = n
		}
	}
	return l, nil
}
func (s *Store) Limits() Limits { return s.limits }

// A dedicated connection keeps BEGIN IMMEDIATE, all checks, and the commit under
// the same SQLite write lock, including across separate processes.
func (s *Store) write(ctx context.Context, f func(*sql.Conn) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	if err = f(conn); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}
