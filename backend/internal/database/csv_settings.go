package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"
)

type CSVColumn struct {
	Header string `json:"header,omitempty"`
	Index  int    `json:"index,omitempty"`
}
type CSVSettings struct {
	Encoding           string                `json:"encoding"`
	Delimiter          string                `json:"delimiter"`
	QuoteChar          string                `json:"quoteChar"`
	HeaderRow          bool                  `json:"headerRow"`
	SkipLines          int                   `json:"skipLines"`
	DecimalSeparator   string                `json:"decimalSeparator"`
	ThousandsSeparator string                `json:"thousandsSeparator"`
	DateFormat         string                `json:"dateFormat"`
	StripEuro          bool                  `json:"stripEuro"`
	Mapping            map[string]*CSVColumn `json:"mapping"`
}

var csvFields = []string{"isin", "wkn", "ticker", "name", "quantity", "price", "date", "total"}

func DefaultCSVSettings() CSVSettings {
	m := map[string]*CSVColumn{}
	for i, h := range []string{"ISIN", "WKN", "Symbol", "Name", "Amount", "Open Price", "Date", "Total Invest"} {
		m[csvFields[i]] = &CSVColumn{Header: h}
	}
	return CSVSettings{"auto", ";", "\"", true, 0, ",", ".", "DD.MM.YYYY", true, m}
}
func choice(v string, choices ...string) bool {
	for _, c := range choices {
		if v == c {
			return true
		}
	}
	return false
}

// Requests contain a complete configuration. Stored configurations instead pass
// through migrateCSVSettings, which deliberately supports missing older keys.
func DecodeCSVSettings(raw []byte) (CSVSettings, error) {
	var settings CSVSettings
	var fields map[string]json.RawMessage
	problems := map[string]string{}
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return settings, &ValidationError{"INVALID_SETTINGS", "Invalid import configuration.", map[string]string{"settings": "Provide a configuration object."}}
	}
	for _, key := range []string{"encoding", "delimiter", "quoteChar", "headerRow", "skipLines", "decimalSeparator", "thousandsSeparator", "dateFormat", "stripEuro", "mapping"} {
		value, exists := fields[key]
		if !exists || string(value) == "null" {
			problems[key] = "This setting is required."
		}
	}
	var mapping map[string]json.RawMessage
	if err := json.Unmarshal(fields["mapping"], &mapping); err != nil {
		problems["mapping"] = "Provide column mappings."
	}
	for field, value := range mapping {
		if string(value) == "null" {
			continue
		}
		var column map[string]json.RawMessage
		if err := json.Unmarshal(value, &column); err != nil || len(column) != 1 {
			problems["mapping."+field] = "Map one header or column index."
			continue
		}
		header, hasHeader := column["header"]
		index, hasIndex := column["index"]
		if !hasHeader && !hasIndex || string(header) == "null" || string(index) == "null" {
			problems["mapping."+field] = "Map one header or column index."
		}
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		problems["settings"] = "A setting has an invalid value type."
	}
	if len(problems) > 0 {
		return settings, &ValidationError{"INVALID_SETTINGS", "Check the import configuration.", problems}
	}
	return settings, settings.Validate()
}
func (s CSVSettings) Validate() error {
	e := map[string]string{}
	if !choice(s.Encoding, "auto", "utf-8", "windows-1252") {
		e["encoding"] = "Choose Auto, UTF-8 or Windows-1252."
	}
	if utf8.RuneCountInString(s.Delimiter) != 1 || strings.ContainsAny(s.Delimiter, "\r\n\x00") || s.Delimiter == "�" {
		e["delimiter"] = "Choose one delimiter character."
	}
	if !choice(s.QuoteChar, "\"", "'", "") {
		e["quoteChar"] = "Invalid quote character."
	}
	if s.SkipLines < 0 || s.SkipLines > 20 {
		e["skipLines"] = "Choose 0–20 lines."
	}
	if !choice(s.DecimalSeparator, ",", ".") {
		e["decimalSeparator"] = "Choose comma or dot."
	}
	if !choice(s.ThousandsSeparator, ".", ",", " ", "'", "") || s.ThousandsSeparator == s.DecimalSeparator {
		e["thousandsSeparator"] = "Choose a different thousands separator."
	}
	if s.Delimiter == s.DecimalSeparator || s.Delimiter == s.ThousandsSeparator || s.Delimiter == s.QuoteChar {
		e["delimiter"] = "Delimiter must differ from numeric separators and quote character."
	}
	if !choice(s.DateFormat, "DD.MM.YYYY", "YYYY-MM-DD", "DD/MM/YYYY", "MM/DD/YYYY") {
		e["dateFormat"] = "Invalid date format."
	}
	seen := map[string]bool{}
	for _, f := range csvFields {
		c := s.Mapping[f]
		if c == nil {
			continue
		}
		h := strings.ToLower(strings.TrimSpace(c.Header))
		key := "h:" + h
		if (h == "") == (c.Index == 0) || c.Index < 0 || c.Index > 10000 {
			e["mapping."+f] = "Map a header or a positive 1-based column index."
		}
		if c.Index > 0 {
			b, _ := json.Marshal(c.Index)
			key = "i:" + string(b)
		}
		if seen[key] {
			e["mapping."+f] = "A column can only be mapped once."
		}
		seen[key] = true
	}
	if s.Mapping["isin"] == nil && s.Mapping["wkn"] == nil {
		e["mapping.isin"] = "Map ISIN or WKN."
	}
	for _, f := range []string{"quantity", "price", "date"} {
		if s.Mapping[f] == nil {
			e["mapping."+f] = "This mapping is required."
		}
	}
	if len(e) > 0 {
		return &ValidationError{"INVALID_SETTINGS", "Check the import configuration.", e}
	}
	return nil
}

type CSVSettingsResult struct {
	Settings  CSVSettings `json:"settings"`
	Saved     bool        `json:"saved"`
	UpdatedAt string      `json:"updatedAt,omitempty"`
}

// Version 0 predates explicit versioning and uses the same keys. Decode over defaults
// to migrate missing keys; ignore unknown keys so older clients remain compatible.
func migrateCSVSettings(version int, raw string) (CSVSettings, error) {
	s := DefaultCSVSettings()
	if version < 0 || version > 1 || strings.TrimSpace(raw) == "null" {
		return s, errors.New("unsupported settings version")
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return s, err
	}
	for key := range s.Mapping {
		if !choice(key, csvFields...) {
			delete(s.Mapping, key)
		}
	}
	return s, s.Validate()
}
func (s *Store) CSVSettings(ctx context.Context, user string) (CSVSettingsResult, error) {
	result := CSVSettingsResult{Settings: DefaultCSVSettings()}
	var raw string
	var version int
	err := s.db.QueryRowContext(ctx, "SELECT settings,schema_version,updated_at FROM csv_import_settings WHERE user_id=?", user).Scan(&raw, &version, &result.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	config, err := migrateCSVSettings(version, raw)
	if err != nil {
		slog.Warn("Unreadable CSV import settings", "user", user, "error", err)
		result.UpdatedAt = ""
		return result, nil
	}
	result.Settings = config
	result.Saved = true
	return result, nil
}
func saveCSVSettings(ctx context.Context, conn *sql.Conn, user string, s CSVSettings) (CSVSettingsResult, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return CSVSettingsResult{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = conn.ExecContext(ctx, `INSERT INTO csv_import_settings(user_id,schema_version,settings,updated_at) VALUES (?,1,?,?) ON CONFLICT(user_id) DO UPDATE SET schema_version=1,settings=excluded.settings,updated_at=excluded.updated_at`, user, string(raw), now)
	return CSVSettingsResult{s, true, now}, err
}
func (s *Store) SaveCSVSettings(ctx context.Context, user string, settings CSVSettings) (CSVSettingsResult, error) {
	var result CSVSettingsResult
	if err := settings.Validate(); err != nil {
		return result, err
	}
	err := s.write(ctx, func(conn *sql.Conn) error {
		var err error
		result, err = saveCSVSettings(ctx, conn, user, settings)
		return err
	})
	return result, err
}
