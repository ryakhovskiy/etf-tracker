package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../../../testdata/import/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func importPortfolio(t *testing.T) (*Store, int64) {
	t.Helper()
	s := featureStore(t)
	p, e := s.SavePortfolio(context.Background(), "alice", 0, "Import")
	if e != nil {
		t.Fatal(e)
	}
	return s, p.ID
}
func sampleRow() ImportRow {
	return ImportRow{RowNumber: 2, Include: true, Fields: ImportFields{ISIN: "IE00B53SZB19", WKN: "A0YED1", Name: "HSBC UCITS", Quantity: "7.79277", Price: "32.08", Total: "249.99", Date: "2024-06-01"}}
}
func validateRows(t *testing.T, s *Store, id int64, rows ...ImportRow) ImportPreview {
	t.Helper()
	v, e := s.ValidateImport(context.Background(), "alice", id, rows)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func commitPreview(s *Store, id int64, v ImportPreview) (ImportResult, error) {
	return s.CommitImport(context.Background(), "alice", id, ImportInput{Rows: v.Rows, Settings: DefaultCSVSettings(), Revision: v.Revision})
}
func TestCSVReference(t *testing.T) {
	for _, file := range []string{"reference.csv", "reference-windows-1252.csv"} {
		t.Run(file, func(t *testing.T) {
			s, id := importPortfolio(t)
			v, e := s.ParseImport(context.Background(), "alice", id, fixture(t, file), DefaultCSVSettings())
			if e != nil {
				t.Fatal(e)
			}
			if v.Summary.Errors != 0 || len(v.Rows) != 3 {
				t.Fatalf("%+v", v)
			}
			for i, want := range []ImportFields{{Quantity: "7.79277", Price: "32.08", Total: "249.99", Date: "2024-06-01"}, {Quantity: "17", Price: "29.65", Total: "503.97", Date: "2024-05-01"}, {Quantity: "100", Price: "62.31", Total: "6231.43", Date: "2024-09-01"}} {
				got := v.Rows[i].Fields
				if got.Quantity != want.Quantity || got.Price != want.Price || got.Total != want.Total || got.Date != want.Date || got.Type != "ETF" {
					t.Fatalf("%+v", got)
				}
			}
			result, e := commitPreview(s, id, v)
			if e != nil || result.LinesCreated != 3 || result.TransactionsCreated != 3 || result.InstrumentsCreated != 0 || result.ItemsAdded != 3 {
				t.Fatalf("%+v %v", result, e)
			}
			var n int
			s.db.QueryRow("SELECT COUNT(*) FROM instruments WHERE source='CSV'").Scan(&n)
			if n != 0 {
				t.Fatal(n)
			}
			settings, e := s.CSVSettings(context.Background(), "alice")
			if e != nil || !settings.Saved {
				t.Fatal(settings, e)
			}
		})
	}
}
func TestCSVParserOptions(t *testing.T) {
	for _, delimiter := range []string{";", "|", "\t", ":"} {
		for _, quote := range []string{"\"", "'", ""} {
			t.Run(fmt.Sprintf("%q-%q", delimiter, quote), func(t *testing.T) {
				s := DefaultCSVSettings()
				s.Delimiter = delimiter
				s.QuoteChar = quote
				text := strings.Join([]string{"ISIN", "Amount", "Open Price", "Date", "Total Invest"}, delimiter) + "\n" + strings.Join([]string{"IE00B4X9L533", "2", "1.592,00 €", "01.06.2024", "3.184,00 €"}, delimiter)
				if quote != "" {
					text = strings.Replace(text, "1.592,00 €", quote+"1.592,00 €"+quote, 1)
				}
				_, rows, e := ParseCSV([]byte(text), s)
				if e != nil || len(rows) != 1 || rows[0].Fields.Price != "1592.00" || rows[0].Fields.Total != "3184.00" {
					t.Fatalf("%+v %v", rows, e)
				}
			})
		}
	}
	for _, decimal := range []string{",", "."} {
		for _, thousands := range []string{".", ",", " ", "'", ""} {
			s := DefaultCSVSettings()
			s.DecimalSeparator = decimal
			s.ThousandsSeparator = thousands
			if decimal == thousands {
				if s.Validate() == nil {
					t.Fatal("equal separators accepted")
				}
				continue
			}
			raw := "1" + thousands + "234" + decimal + "50 €"
			if got := csvNumber(raw, s); got != "1234.50" {
				t.Fatalf("%q => %q", raw, got)
			}
		}
	}
	s := DefaultCSVSettings()
	s.Delimiter = ","
	s.DecimalSeparator = "."
	s.ThousandsSeparator = ""
	if s.Validate() != nil {
		t.Fatal("comma delimiter rejected")
	}
	for format, raw := range map[string]string{"DD.MM.YYYY": "02.03.2024", "YYYY-MM-DD": "2024-03-02", "DD/MM/YYYY": "02/03/2024", "MM/DD/YYYY": "03/02/2024"} {
		s := DefaultCSVSettings()
		s.DateFormat = format
		s.HeaderRow = false
		s.SkipLines = 1
		s.Mapping = map[string]*CSVColumn{"isin": {Index: 1}, "quantity": {Index: 2}, "price": {Index: 3}, "date": {Index: 4}, "total": nil}
		_, r, e := ParseCSV([]byte("skip me\nIE00B4X9L533;2;1,005;"+raw+"\n\n"), s)
		if e != nil || len(r) != 1 || r[0].Fields.Date != "2024-03-02" || r[0].Fields.Total != "2.01" {
			t.Fatal(r, e)
		}
	}
	s = DefaultCSVSettings()
	text := "\ufeff isin ; Amount ;Open Price;Date;Name\r\nIE00B4X9L533;2;3;01.01.2024;\"An; ETF\nwith \"\"quotes\"\"\"\r\n"
	_, r, e := ParseCSV([]byte(text), s)
	if e != nil || r[0].Fields.Name != "An; ETF\nwith \"quotes\"" {
		t.Fatal(r, e)
	}
	for _, text := range []string{"a;\"unterminated", "a;\"quoted\"x", "a;b\"c"} {
		if _, _, e = ParseCSV([]byte(text), s); e == nil {
			t.Fatal("accepted malformed CSV")
		}
	}
	if _, _, e = ParseCSV(make([]byte, CSVMaxBytes+1), s); e == nil {
		t.Fatal("accepted large file")
	}
	if _, _, e = ParseCSV([]byte("ISIN\n"+strings.Repeat("IE00B4X9L533\n", 5001)), s); e == nil {
		t.Fatal("accepted too many rows")
	}
	if _, _, e = ParseCSV([]byte("ISIN\n"+strings.Repeat("IE00B4X9L533\n", 5000)), s); e != nil {
		t.Fatal(e)
	}
	for _, strip := range []bool{true, false} {
		s.StripEuro = strip
		got := csvNumber("12,50 EUR", s)
		if (numberError(got, 4) == "") != strip {
			t.Fatal(got)
		}
	}
	for _, v := range []string{"$12.50", "12.50 USD", "£12.50"} {
		if numberError(v, 4) != "Prices must be in EUR" {
			t.Fatal(v)
		}
	}
	s.Encoding = "utf-8"
	if _, _, e = ParseCSV(fixture(t, "reference-windows-1252.csv"), s); e == nil {
		t.Fatal("accepted invalid UTF8")
	}
}
func TestCSVSettingsPersistence(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	v, e := s.CSVSettings(ctx, "alice")
	if e != nil || v.Saved || !reflect.DeepEqual(v.Settings, DefaultCSVSettings()) {
		t.Fatal(v, e)
	}
	settings := DefaultCSVSettings()
	settings.Delimiter = "|"
	saved, e := s.SaveCSVSettings(ctx, "alice", settings)
	if e != nil || !saved.Saved || saved.UpdatedAt == "" {
		t.Fatal(saved, e)
	}
	v, e = s.CSVSettings(ctx, "alice")
	if e != nil || !reflect.DeepEqual(v, saved) {
		t.Fatal(v, e)
	}
	other, _ := s.CSVSettings(ctx, "bob")
	if other.Saved {
		t.Fatal("settings leaked")
	}
	again, _ := s.SaveCSVSettings(ctx, "alice", settings)
	if again.UpdatedAt == saved.UpdatedAt {
		t.Fatal("timestamp not updated")
	}
	for _, raw := range []string{"bad json", "null", `{"delimiter":","}`} {
		s.db.Exec("UPDATE csv_import_settings SET settings=? WHERE user_id='alice'", raw)
		v, e = s.CSVSettings(ctx, "alice")
		if e != nil || v.Saved {
			t.Fatal(v, e)
		}
	}
	s.db.Exec(`UPDATE csv_import_settings SET schema_version=0,settings='{"unknown":1,"delimiter":"|","mapping":{"isin":{"header":"ISIN"}}}' WHERE user_id='alice'`)
	v, e = s.CSVSettings(ctx, "alice")
	if e != nil || !v.Saved || v.Settings.Delimiter != "|" || v.Settings.Mapping["price"] == nil {
		t.Fatal(v, e)
	}
	s.db.Exec("DELETE FROM users WHERE id='alice'")
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM csv_import_settings").Scan(&n)
	if n != 0 {
		t.Fatal("settings did not cascade")
	}
}
func TestCSVSettingsInvalid(t *testing.T) {
	cases := []func(*CSVSettings){func(s *CSVSettings) { s.Encoding = "latin1" }, func(s *CSVSettings) { s.Delimiter = "xx" }, func(s *CSVSettings) { s.Delimiter = "\n" }, func(s *CSVSettings) { s.Delimiter = "." }, func(s *CSVSettings) { s.Delimiter = "\"" }, func(s *CSVSettings) { s.QuoteChar = "|" }, func(s *CSVSettings) { s.SkipLines = 21 }, func(s *CSVSettings) { s.SkipLines = -1 }, func(s *CSVSettings) { s.DecimalSeparator = "x" }, func(s *CSVSettings) { s.ThousandsSeparator = "," }, func(s *CSVSettings) { s.ThousandsSeparator = "x" }, func(s *CSVSettings) { s.DateFormat = "today" }, func(s *CSVSettings) { s.Mapping["isin"] = nil; s.Mapping["wkn"] = nil }, func(s *CSVSettings) { s.Mapping["quantity"] = nil }, func(s *CSVSettings) { s.Mapping["date"] = nil }, func(s *CSVSettings) { s.Mapping["price"] = nil }, func(s *CSVSettings) { s.Mapping["price"] = &CSVColumn{Header: " Amount "} }, func(s *CSVSettings) { s.Mapping["price"] = &CSVColumn{Index: -1} }, func(s *CSVSettings) { s.Mapping["price"] = &CSVColumn{Index: 2, Header: "Price"} }, func(s *CSVSettings) { s.Mapping["price"] = &CSVColumn{} }, func(s *CSVSettings) {
		s.Mapping["price"] = &CSVColumn{Index: 2}
		s.Mapping["date"] = &CSVColumn{Index: 2}
	}}
	for i, change := range cases {
		s := DefaultCSVSettings()
		change(&s)
		var validation *ValidationError
		if e := s.Validate(); !errors.As(e, &validation) || validation.Code != "INVALID_SETTINGS" || len(validation.Fields) == 0 {
			t.Fatalf("case %d: %v", i, e)
		}
	}
}
func TestCSVResolutionAndDuplicates(t *testing.T) {
	s, id := importPortfolio(t)
	ctx := context.Background()
	row := sampleRow()
	row.Fields.ISIN = "US0378331005"
	row.Fields.WKN = "865985"
	v := validateRows(t, s, id, row)
	if v.Rows[0].Status != "WILL_ADD_TO_PORTFOLIO" {
		t.Fatal(v)
	}
	s.AddItems(ctx, "alice", id, []int64{1})
	v = validateRows(t, s, id, row)
	if v.Rows[0].Status != "OK" {
		t.Fatal(v)
	}
	row.Fields.WKN = "870747"
	v = validateRows(t, s, id, row)
	if v.Rows[0].Status != "ERROR" {
		t.Fatal("missing conflict")
	}
	row.Fields.WKN = "XXXXXX"
	v = validateRows(t, s, id, row)
	if len(v.Rows[0].Warnings) == 0 || v.Rows[0].Status == "ERROR" {
		t.Fatal(v)
	}
	row.Fields.ISIN = ""
	row.Fields.WKN = "XXXXXX"
	v = validateRows(t, s, id, row)
	if v.Rows[0].Status != "ERROR" {
		t.Fatal("unknown WKN accepted")
	}
	row.Fields.WKN = "865985"
	v = validateRows(t, s, id, row)
	if v.Rows[0].Status != "OK" {
		t.Fatal(v)
	}
	row = sampleRow()
	second := row
	second.RowNumber = 3
	second.Fields.Name = "Other name"
	v = validateRows(t, s, id, row, second)
	if v.Rows[1].Status != "DUPLICATE" || len(v.Rows[1].Warnings) != 1 || v.Summary.UndecidedDuplicates != 1 {
		t.Fatal(v)
	}
	v.Rows[1].Decision = "skip"
	v = validateRows(t, s, id, v.Rows...)
	result, e := commitPreview(s, id, v)
	if e != nil || result.InstrumentsCreated != 1 || result.LinesCreated != 1 || result.Skipped != 1 {
		t.Fatal(result, e)
	}
	v = validateRows(t, s, id, row)
	if v.Rows[0].Status != "DUPLICATE" {
		t.Fatal(v)
	}
	v.Rows[0].Decision = "import"
	v = validateRows(t, s, id, v.Rows...)
	if _, e = commitPreview(s, id, v); e != nil {
		t.Fatal(e)
	}
	s.db.Exec("UPDATE instruments SET wkn=NULL WHERE isin=?", row.Fields.ISIN)
	row.Fields.Date = "2024-07-01"
	v = validateRows(t, s, id, row)
	if _, e = commitPreview(s, id, v); e != nil {
		t.Fatal(e)
	}
	var wkn string
	s.db.QueryRow("SELECT wkn FROM instruments WHERE isin=?", row.Fields.ISIN).Scan(&wkn)
	if wkn != "A0YED1" {
		t.Fatal(wkn)
	}
}
func TestCSVImportAtomicStaleAndLimits(t *testing.T) {
	s, id := importPortfolio(t)
	ctx := context.Background()
	one := sampleRow()
	two := one
	two.RowNumber = 3
	two.Fields.Date = "2024-07-01"
	v := validateRows(t, s, id, one, two)
	if _, e := s.db.Exec(`CREATE TRIGGER fail_csv BEFORE INSERT ON transaction_lines WHEN (SELECT count(*) FROM transaction_lines)=1 BEGIN SELECT RAISE(ABORT,'test'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e := commitPreview(s, id, v); e == nil {
		t.Fatal("expected failure")
	}
	for _, table := range []string{"transactions", "transaction_lines", "portfolio_items", "csv_import_settings"} {
		var n int
		s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n)
		if n != 0 {
			t.Fatal("partial write", table, n)
		}
	}
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM instruments WHERE source='CSV'").Scan(&n)
	if n != 0 {
		t.Fatal("partial instrument write")
	}
	s.db.Exec("DROP TRIGGER fail_csv")
	if _, e := commitPreview(s, id, v); e != nil {
		t.Fatal(e)
	}
	_, e := commitPreview(s, id, v)
	var stale *ImportError
	if !errors.As(e, &stale) || stale.Code != "IMPORT_STALE" || stale.Preview.Rows[0].Status != "DUPLICATE" {
		t.Fatal(e)
	}
	s.limits.Items = 1
	third := one
	third.Fields.ISIN = "US0378331005"
	third.Fields.WKN = "865985"
	v = validateRows(t, s, id, third)
	_, e = commitPreview(s, id, v)
	var limit *LimitError
	if !errors.As(e, &limit) || limit.Code != "ITEM_LIMIT_REACHED" {
		t.Fatal(e)
	}
	if _, e = s.ValidateImport(ctx, "bob", id, []ImportRow{one}); e != ErrNotFound {
		t.Fatal(e)
	}
	if _, e = s.CommitImport(ctx, "bob", id, ImportInput{Rows: []ImportRow{one}, Settings: DefaultCSVSettings()}); e != ErrNotFound {
		t.Fatal(e)
	}
}
func TestCSVWarningsAndEditing(t *testing.T) {
	s, id := importPortfolio(t)
	for _, tc := range []struct {
		q, p, total string
		warning     bool
	}{{"1", "1", "1.05", false}, {"1", "1", "1.06", true}, {"1", "999", "1000", false}, {"1", "998.99", "1000", true}} {
		r := sampleRow()
		r.Fields.Quantity = tc.q
		r.Fields.Price = tc.p
		r.Fields.Total = tc.total
		v := validateRows(t, s, id, r)
		if (len(v.Rows[0].Warnings) > 0) != tc.warning {
			t.Fatal(tc, v.Rows[0].Warnings)
		}
	}
	for _, tc := range []struct{ field, value string }{{"quantity", "0"}, {"quantity", "1.0000001"}, {"price", "1000001"}, {"price", "USD 12"}, {"total", "-1"}, {"date", "2099-01-01"}, {"date", "1989-01-01"}, {"isin", "IE00B53SZB18"}} {
		r := sampleRow()
		b, _ := json.Marshal(r.Fields)
		var fields map[string]string
		json.Unmarshal(b, &fields)
		fields[tc.field] = tc.value
		b, _ = json.Marshal(fields)
		json.Unmarshal(b, &r.Fields)
		v := validateRows(t, s, id, r)
		if v.Rows[0].Status != "ERROR" {
			t.Fatal(tc, v)
		}
		r = sampleRow()
		v = validateRows(t, s, id, r)
		if v.Rows[0].Status != "NEW_INSTRUMENT" {
			t.Fatal(v)
		}
	}
}

func generatedISIN(n int) string {
	base := fmt.Sprintf("DE%09d", n)
	for digit := 0; digit < 10; digit++ {
		s := fmt.Sprint(base, digit)
		if validISIN(s) {
			return s
		}
	}
	panic("check digit")
}
func TestCSVNewInstrumentCapAndMetadata(t *testing.T) {
	s, id := importPortfolio(t)
	s.limits.Items = 200
	rows := []ImportRow{}
	for i := 0; i < 101; i++ {
		r := sampleRow()
		r.RowNumber = i + 1
		r.Fields.ISIN = generatedISIN(i)
		r.Fields.WKN = ""
		r.Fields.Name = "Ordinary company"
		rows = append(rows, r)
	}
	v := validateRows(t, s, id, rows...)
	if v.Summary.Errors != 101 {
		t.Fatal(v.Summary)
	}
	if _, e := commitPreview(s, id, v); e == nil {
		t.Fatal("cap not enforced")
	}
	for _, name := range []string{"Ordinary company", "fund ucits", "Example ETC", "ETF fund"} {
		r := rows[0]
		r.Fields.Name = name
		r.Fields.Type = ""
		v = validateRows(t, s, id, r)
		want := "ETF"
		if name == "Ordinary company" {
			want = "STOCK"
		}
		if v.Rows[0].Fields.Type != want {
			t.Fatal(v)
		}
	}
	first := rows[0]
	first.Fields.Ticker = "FIRST"
	second := first
	second.RowNumber = 999
	second.Fields.Date = "2024-08-01"
	second.Fields.Ticker = "SECOND"
	second.Fields.Name = "Other name"
	v = validateRows(t, s, id, first, second)
	result, e := commitPreview(s, id, v)
	if e != nil || result.InstrumentsCreated != 1 || result.LinesCreated != 2 || result.TransactionsCreated != 2 {
		t.Fatal(result, e)
	}
	var name, ticker, source string
	s.db.QueryRow("SELECT name,ticker,source FROM instruments WHERE isin=?", first.Fields.ISIN).Scan(&name, &ticker, &source)
	if name != first.Fields.Name || ticker != "FIRST" || source != "CSV" {
		t.Fatal(name, ticker, source)
	}
}
func TestCSVOneTransactionPerDateAndExcludedErrors(t *testing.T) {
	s, id := importPortfolio(t)
	one := sampleRow()
	two := one
	two.RowNumber = 3
	two.Fields.Quantity = "2"
	two.Fields.Total = "64.16"
	bad := one
	bad.RowNumber = 4
	bad.Fields.Quantity = "broken"
	bad.Include = false
	v := validateRows(t, s, id, one, two, bad)
	if v.Summary.Errors != 1 || v.Summary.Transactions != 1 {
		t.Fatal(v.Summary)
	}
	result, e := commitPreview(s, id, v)
	if e != nil || result.LinesCreated != 2 || result.TransactionsCreated != 1 {
		t.Fatal(result, e)
	}
}

func TestCSVSettingsRequestTypes(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"headerRow":null}`, `[]`} {
		if _, err := DecodeCSVSettings([]byte(raw)); err == nil {
			t.Fatal(raw)
		}
	}
	good, _ := json.Marshal(DefaultCSVSettings())
	for _, key := range []string{"headerRow", "stripEuro", "skipLines", "mapping", "encoding"} {
		var fields map[string]json.RawMessage
		json.Unmarshal(good, &fields)
		fields[key] = json.RawMessage("null")
		raw, _ := json.Marshal(fields)
		if _, e := DecodeCSVSettings(raw); e == nil {
			t.Fatal("accepted null", key)
		}
		delete(fields, key)
		raw, _ = json.Marshal(fields)
		if _, e := DecodeCSVSettings(raw); e == nil {
			t.Fatal("accepted missing", key)
		}
	}
	if _, e := DecodeCSVSettings(good); e != nil {
		t.Fatal(e)
	}
}
