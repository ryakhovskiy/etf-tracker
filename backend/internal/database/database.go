package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

type User struct {
	ID        string
	GoogleID  string
	Email     string
	Name      string
	Picture   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Store struct {
	stopCleanup context.CancelFunc
	db          *sql.DB
	limits      Limits
	quoteTTLNS  atomic.Int64
}

func Open(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
		// SQLite inherits the database mode when creating WAL/SHM files. Set it
		// before opening SQLite, and repair sidecars from older installations.
		if err := secureDatabaseFiles(path); err != nil {
			return nil, err
		}
	}
	limits, err := loadLimits()
	if err != nil {
		return nil, err
	}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	db, err := sql.Open("sqlite", path+separator+"_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, limits: limits}
	if err := store.configure(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.CleanupAV(context.Background(), time.Now()); err != nil {
		db.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	store.stopCleanup = cancel
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				_ = store.CleanupAV(ctx, now)
			}
		}
	}()
	return store, nil
}

func secureDatabaseFiles(path string) error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		name := path + suffix
		info, err := os.Lstat(name)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect database file: %w", err)
		}
		if err == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("database file must be a regular file: %s", name)
		}
		if suffix != "" && errors.Is(err, os.ErrNotExist) {
			continue
		}
		file, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return fmt.Errorf("open database file securely: %w", err)
		}
		err = file.Chmod(0o600)
		closeErr := file.Close()
		if err != nil {
			return fmt.Errorf("restrict database permissions: %w", err)
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func (s *Store) Close() error {
	if s.stopCleanup != nil {
		s.stopCleanup()
	}
	return s.db.Close()
}

func (s *Store) configure(ctx context.Context) error {
	for _, statement := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure sqlite: %s: %w", statement, err)
		}
	}
	return nil
}

func (s *Store) UpsertGoogleUser(ctx context.Context, googleID, email, name, picture string) (User, error) {
	now := time.Now().UTC().Unix()
	id := googleID
	err := s.write(ctx, func(conn *sql.Conn) error {
		var exists, current int
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE google_id = ?", googleID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&current); err != nil {
				return err
			}
			if current >= s.limits.Users {
				return limitError("USER_LIMIT_REACHED", s.limits.Users, current)
			}
		}
		_, err := conn.ExecContext(ctx, `
INSERT INTO users (id, google_id, email, name, picture, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(google_id) DO UPDATE SET email=excluded.email, name=excluded.name, picture=excluded.picture, updated_at=excluded.updated_at`,
			id, googleID, email, name, picture, now, now)
		return err
	})
	if err != nil {
		return User{}, fmt.Errorf("upsert user: %w", err)
	}
	return s.UserByID(ctx, id)
}

func (s *Store) UserByID(ctx context.Context, id string) (User, error) {
	var user User
	var created, updated int64
	err := s.db.QueryRowContext(ctx, `SELECT id, google_id, email, name, picture, created_at, updated_at FROM users WHERE id = ?`, id).
		Scan(&user.ID, &user.GoogleID, &user.Email, &user.Name, &user.Picture, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("get user: %w", err)
	}
	user.CreatedAt = time.Unix(created, 0).UTC()
	user.UpdatedAt = time.Unix(updated, 0).UTC()
	return user, nil
}

var ErrNotFound = errors.New("not found")

func HashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func (s *Store) CreateRefreshToken(ctx context.Context, tokenID, userID, tokenHash string, expiresAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin session: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `INSERT INTO sessions (id, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`, tokenID, userID, expiresAt.Unix(), now); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at, created_at, session_id) VALUES (?, ?, ?, ?, ?, ?)`, tokenID, userID, tokenHash, expiresAt.Unix(), now, tokenID)
	if err != nil {
		return fmt.Errorf("create refresh token: %w", err)
	}
	return tx.Commit()
}

func (s *Store) RotateRefreshToken(ctx context.Context, oldHash, newID, newHash string, expiresAt time.Time) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin refresh rotation: %w", err)
	}
	defer tx.Rollback()

	var userID, sessionID string
	var tokenExpires, sessionExpires int64
	var tokenRevoked, sessionRevoked sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT r.user_id, r.expires_at, r.revoked_at, s.id, s.expires_at, s.revoked_at
FROM refresh_tokens r JOIN sessions s ON s.id = r.session_id
WHERE r.token_hash = ?`, oldHash).Scan(&userID, &tokenExpires, &tokenRevoked, &sessionID, &sessionExpires, &sessionRevoked)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("find refresh token: %w", err)
	}
	now := time.Now().UTC().Unix()
	if sessionRevoked.Valid {
		return User{}, ErrNotFound
	}
	if tokenRevoked.Valid {
		// Reuse of an already rotated token compromises the entire session.
		// Commit revocation before reporting the authentication failure.
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE id = ?`, now, sessionID); err != nil {
			return User{}, fmt.Errorf("revoke replayed session: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return User{}, fmt.Errorf("commit replay revocation: %w", err)
		}
		return User{}, ErrNotFound
	}
	if now >= tokenExpires || now >= sessionExpires {
		return User{}, ErrNotFound
	}
	if expiresAt.Unix() > sessionExpires {
		expiresAt = time.Unix(sessionExpires, 0)
	}
	result, err := tx.ExecContext(ctx, `UPDATE refresh_tokens SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL`, now, oldHash)
	if err != nil {
		return User{}, fmt.Errorf("revoke refresh token: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return User{}, ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at, created_at, session_id) VALUES (?, ?, ?, ?, ?, ?)`, newID, userID, newHash, expiresAt.Unix(), now, sessionID); err != nil {
		return User{}, fmt.Errorf("store rotated refresh token: %w", err)
	}
	var user User
	var created, updated int64
	err = tx.QueryRowContext(ctx, `SELECT id, google_id, email, name, picture, created_at, updated_at FROM users WHERE id = ?`, userID).
		Scan(&user.ID, &user.GoogleID, &user.Email, &user.Name, &user.Picture, &created, &updated)
	if err != nil {
		return User{}, fmt.Errorf("load refresh token user: %w", err)
	}
	user.CreatedAt = time.Unix(created, 0).UTC()
	user.UpdatedAt = time.Unix(updated, 0).UTC()
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit refresh rotation: %w", err)
	}
	return user, nil
}

func (s *Store) RevokeRefreshToken(ctx context.Context, tokenHash string) error {
	return s.RevokeSession(ctx, tokenHash, "")
}

// RevokeSession accepts a refresh-token hash and/or a verified JWT ID. An old
// refresh token still identifies its family after rotation.
func (s *Store) RevokeSession(ctx context.Context, tokenHash, accessID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE revoked_at IS NULL AND id IN
(SELECT session_id FROM refresh_tokens WHERE token_hash = ? OR id = ?)`, time.Now().UTC().Unix(), tokenHash, accessID)
	return err
}

func (s *Store) CheckSession(ctx context.Context, accessID, userID string) error {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM sessions s JOIN refresh_tokens r ON r.session_id = s.id
WHERE r.id = ? AND s.user_id = ? AND s.revoked_at IS NULL AND s.expires_at > ?`, accessID, userID, time.Now().UTC().Unix()).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
