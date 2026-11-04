package database

import (
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const CSVMaxBytes = 1024 * 1024
const CSVMaxRows = 5000

type ImportFields struct {
	ISIN     string `json:"isin"`
	WKN      string `json:"wkn"`
	Ticker   string `json:"ticker"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Quantity string `json:"quantity"`
	Price    string `json:"price"`
	Total    string `json:"total"`
	Date     string `json:"date"`
}
type ImportRow struct {
	RowNumber  int               `json:"rowNumber"`
	Include    bool              `json:"include"`
	Fields     ImportFields      `json:"fields"`
	Decision   string            `json:"decision,omitempty"`
	Raw        []string          `json:"raw,omitempty"`
	Status     string            `json:"status"`
	Errors     map[string]string `json:"errors"`
	Warnings   []string          `json:"warnings"`
	Instrument *Instrument       `json:"instrument,omitempty"`
}
type ImportSummary struct {
	Rows                int `json:"rows"`
	Included            int `json:"included"`
	Errors              int `json:"errors"`
	UndecidedDuplicates int `json:"undecidedDuplicates"`
	NewInstruments      int `json:"newInstruments"`
	NewItems            int `json:"newItems"`
	Transactions        int `json:"transactions"`
	Lines               int `json:"lines"`
	ItemsAfter          int `json:"itemsAfter"`
	ItemLimit           int `json:"itemLimit"`
}
type ImportPreview struct {
	Headers  []string      `json:"headers,omitempty"`
	Rows     []ImportRow   `json:"rows"`
	Summary  ImportSummary `json:"summary"`
	Revision string        `json:"revision"`
}

func csvError(message string) error { return &ValidationError{"INVALID_CSV", message, nil} }
func decodeCSV(data []byte, encoding string) (string, error) {
	if len(data) > CSVMaxBytes {
		return "", &ImportError{Code: "FILE_TOO_LARGE", Message: "File must be at most 1 MB."}
	}
	if encoding == "utf-8" || (encoding == "auto" && utf8.Valid(data)) {
		if !utf8.Valid(data) {
			return "", csvError("Invalid UTF-8 file.")
		}
		return strings.TrimPrefix(string(data), "\ufeff"), nil
	}
	const cp = "€\u0081‚ƒ„…†‡ˆ‰Š‹Œ\u008dŽ\u008f\u0090‘’“”•–—˜™š›œ\u009džŸ"
	table := []rune(cp)
	var b strings.Builder
	for _, v := range data {
		if v >= 0x80 && v <= 0x9f {
			b.WriteRune(table[int(v)-0x80])
		} else {
			b.WriteRune(rune(v))
		}
	}
	return b.String(), nil
}

// RFC 4180 records with a configurable delimiter and quote, including embedded
// newlines and doubled quotes. Physical leading lines are skipped before lexing.
func csvRecords(text string, s CSVSettings) ([][]string, error) {
	for i := 0; i < s.SkipLines; i++ {
		p := strings.IndexByte(text, '\n')
		if p < 0 {
			return nil, nil
		}
		text = text[p+1:]
	}
	d := []rune(s.Delimiter)[0]
	var q rune
	if s.QuoteChar != "" {
		q = []rune(s.QuoteChar)[0]
	}
	chars := []rune(text)
	records := [][]string{}
	record := []string{}
	var cell strings.Builder
	quoted, closed, started := false, false, false
	finish := func() { record = append(record, cell.String()); cell.Reset(); closed = false; started = false }
	row := func() {
		finish()
		blank := true
		for _, c := range record {
			if strings.TrimSpace(c) != "" {
				blank = false
			}
		}
		if !blank {
			records = append(records, record)
		}
		record = []string{}
	}
	for i := 0; i < len(chars); i++ {
		c := chars[i]
		if quoted {
			if c == q {
				if i+1 < len(chars) && chars[i+1] == q {
					cell.WriteRune(q)
					i++
				} else {
					quoted = false
					closed = true
				}
			} else {
				cell.WriteRune(c)
			}
			continue
		}
		if c == d {
			finish()
			continue
		}
		if c == '\n' || c == '\r' {
			if c == '\r' && i+1 < len(chars) && chars[i+1] == '\n' {
				i++
			}
			row()
			if len(records) > CSVMaxRows+1 {
				return nil, csvError("At most 5,000 data rows are allowed.")
			}
			continue
		}
		if closed {
			return nil, csvError("Unexpected character after a closing quote.")
		}
		if q != 0 && c == q {
			if started {
				return nil, csvError("Quote inside an unquoted field.")
			}
			quoted = true
			started = true
			continue
		}
		cell.WriteRune(c)
		started = true
	}
	if quoted {
		return nil, csvError("Unclosed quoted field.")
	}
	if started || closed || len(record) > 0 || cell.Len() > 0 {
		row()
	}
	return records, nil
}
func csvNumber(raw string, s CSVSettings) string {
	v := strings.TrimSpace(raw)
	if s.StripEuro {
		v = strings.ReplaceAll(strings.ReplaceAll(v, "€", ""), "EUR", "")
	}
	v = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, v)
	if s.ThousandsSeparator != "" {
		v = strings.ReplaceAll(v, s.ThousandsSeparator, "")
	}
	return strings.ReplaceAll(v, s.DecimalSeparator, ".")
}
func numberError(v string, scale int) string {
	for _, r := range v {
		if unicode.IsLetter(r) || unicode.IsSymbol(r) {
			return "Prices must be in EUR"
		}
	}
	if _, err := parseDecimal(v, scale); err != nil {
		return err.Error()
	}
	return ""
}
func dateLayout(f string) string {
	return map[string]string{"DD.MM.YYYY": "02.01.2006", "YYYY-MM-DD": "2006-01-02", "DD/MM/YYYY": "02/01/2006", "MM/DD/YYYY": "01/02/2006"}[f]
}
func ParseCSV(data []byte, s CSVSettings) ([]string, []ImportRow, error) {
	if err := s.Validate(); err != nil {
		return nil, nil, err
	}
	text, err := decodeCSV(data, s.Encoding)
	if err != nil {
		return nil, nil, err
	}
	records, err := csvRecords(text, s)
	if err != nil {
		return nil, nil, err
	}
	headers := []string{}
	offset := 0
	if s.HeaderRow && len(records) > 0 {
		headers = records[0]
		records = records[1:]
		offset = 1
	}
	if len(records) > CSVMaxRows {
		return nil, nil, csvError("At most 5,000 data rows are allowed.")
	}
	mapping := map[string]int{}
	used := map[int]bool{}
	for _, f := range csvFields {
		c := s.Mapping[f]
		if c == nil {
			continue
		}
		index := c.Index - 1
		if c.Header != "" {
			index = -1
			for i, h := range headers {
				if strings.EqualFold(strings.TrimSpace(h), strings.TrimSpace(c.Header)) {
					if index >= 0 {
						return nil, nil, csvError("Ambiguous header: " + c.Header)
					}
					index = i
				}
			}
		}
		if index >= 0 {
			if used[index] {
				return nil, nil, csvError("Two fields map to the same column.")
			}
			used[index] = true
		}
		mapping[f] = index
	}
	result := make([]ImportRow, 0, len(records))
	for i, record := range records {
		get := func(f string) string {
			n, ok := mapping[f]
			if !ok || n < 0 || n >= len(record) {
				return ""
			}
			return strings.TrimSpace(record[n])
		}
		f := ImportFields{ISIN: get("isin"), WKN: get("wkn"), Ticker: get("ticker"), Name: get("name"), Quantity: csvNumber(get("quantity"), s), Price: csvNumber(get("price"), s), Total: csvNumber(get("total"), s), Date: get("date")}
		if d, e := time.Parse(dateLayout(s.DateFormat), f.Date); e == nil {
			f.Date = d.Format("2006-01-02")
		}
		if s.Mapping["total"] == nil || f.Total == "" {
			q, qe := parseDecimal(f.Quantity, 6)
			p, pe := parseDecimal(f.Price, 4)
			if qe == nil && pe == nil {
				f.Total = money(new(big.Rat).SetFrac(new(big.Int).Mul(big.NewInt(q), big.NewInt(p)), big.NewInt(10000000000)))
			}
		}
		result = append(result, ImportRow{RowNumber: i + 1 + offset + s.SkipLines, Include: true, Fields: f, Raw: record})
	}
	return headers, result, nil
}
func validISIN(s string) bool {
	if len(s) != 12 || s[0] < 'A' || s[0] > 'Z' || s[1] < 'A' || s[1] > 'Z' || s[11] < '0' || s[11] > '9' {
		return false
	}
	digits := ""
	for _, c := range s {
		if c >= 'A' && c <= 'Z' {
			digits += fmt.Sprint(c - 'A' + 10)
		} else if c >= '0' && c <= '9' {
			digits += string(c)
		} else {
			return false
		}
	}
	sum := 0
	double := false
	for i := len(digits) - 1; i >= 0; i-- {
		v := int(digits[i] - '0')
		if double {
			v *= 2
			if v > 9 {
				v -= 9
			}
		}
		sum += v
		double = !double
	}
	return sum%10 == 0
}
