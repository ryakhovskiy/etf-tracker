package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"holdings/backend/internal/database"
	"holdings/backend/internal/marketdata"
)

type favoriteList struct {
	Favorites []database.Favorite `json:"favorites"`
}

func getFavorites(t *testing.T, handler http.Handler, cookies []*http.Cookie) []database.Favorite {
	t.Helper()
	w := featureRequest(handler, "GET", "/api/favorites", "", cookies...)
	var out favoriteList
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Favorites == nil {
		t.Fatalf("GET /api/favorites: %d %s", w.Code, w.Body.String())
	}
	return out.Favorites
}

func favoriteIDs(list []database.Favorite) []int64 {
	ids := []int64{}
	for i, f := range list {
		if f.Position != i {
			ids = append(ids, -1)
		}
		ids = append(ids, f.InstrumentID)
	}
	return ids
}

func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	var e struct{ Error struct{ Code string } }
	if json.Unmarshal(body, &e) != nil {
		t.Fatalf("not an error body: %s", body)
	}
	return e.Error.Code
}

func TestFavoriteRoutesRequireSession(t *testing.T) {
	_, handler := auditHandler(t, ":memory:")
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/favorites", ""},
		{"PUT", "/api/favorites/1", ""},
		{"DELETE", "/api/favorites/1", ""},
		{"PUT", "/api/favorites/order", `{"instrumentIds":[]}`},
	} {
		if w := featureRequest(handler, tc.method, tc.path, tc.body); w.Code != 401 {
			t.Fatalf("%s %s: %d", tc.method, tc.path, w.Code)
		}
	}
}

func TestFavoriteRoutesStarOrderUnstarAndIsolation(t *testing.T) {
	h, handler, cookies, y, _, _ := yahooPrimary(t)
	pid := summaryPortfolio(t, h, "google-user-1", "Mine", []int64{7, 18, 2}, nil)
	if _, err := h.store.UpsertGoogleUser(context.Background(), "foreign", "foreign@example.invalid", "", ""); err != nil {
		t.Fatal(err)
	}
	fpid := summaryPortfolio(t, h, "foreign", "Secret", []int64{6, 7}, nil)
	if err := h.store.ReorderFavorites(context.Background(), "foreign", []int64{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.StarFavorite(context.Background(), "foreign", 6); err != nil {
		t.Fatal(err)
	}

	if got := getFavorites(t, handler, cookies); len(got) != 0 {
		t.Fatalf("start empty, got %+v", got)
	}
	for i, id := range []int{18, 7} {
		w := featureRequest(handler, "PUT", fmt.Sprintf("/api/favorites/%d", id), "", cookies...)
		var out struct{ InstrumentID, Position int }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.InstrumentID != id || out.Position != i {
			t.Fatalf("star %d: %d %s", id, w.Code, w.Body.String())
		}
	}
	if w := featureRequest(handler, "PUT", "/api/favorites/18", "", cookies...); w.Code != 200 {
		t.Fatalf("star is idempotent: %d", w.Code)
	}
	list := getFavorites(t, handler, cookies)
	if !reflect.DeepEqual(favoriteIDs(list), []int64{18, 7}) || list[0].Name == "" || list[0].Ticker == "" || list[0].Reasons == nil {
		t.Fatalf("list %+v", list)
	}

	// Manage items reads the flag per item, for the caller only.
	w := featureRequest(handler, "GET", fmt.Sprintf("/api/portfolios/%d/items", pid), "", cookies...)
	var items []struct {
		InstrumentID int64
		Favorite     bool
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &items) != nil {
		t.Fatalf("items %d %s", w.Code, w.Body.String())
	}
	flags := map[int64]bool{}
	for _, it := range items {
		flags[it.InstrumentID] = it.Favorite
	}
	if !reflect.DeepEqual(flags, map[int64]bool{7: true, 18: true, 2: false}) {
		t.Fatalf("favorite flags %v", flags)
	}
	// The foreign user's star on 6 is invisible, and so is their portfolio.
	if w := featureRequest(handler, "GET", fmt.Sprintf("/api/portfolios/%d/items", fpid), "", cookies...); w.Code != 404 {
		t.Fatalf("foreign items: %d", w.Code)
	}
	if got := favoriteIDs(getFavorites(t, handler, cookies)); !reflect.DeepEqual(got, []int64{18, 7}) {
		t.Fatalf("foreign favorite leaked %v", got)
	}

	// 404 for anything the caller does not hold, and for malformed ids.
	for _, id := range []string{"6", "9999", "abc", "0", "-1", "order2"} {
		if w := featureRequest(handler, "PUT", "/api/favorites/"+id, "", cookies...); w.Code != 404 || errorCode(t, w.Body.Bytes()) != "NOT_FOUND" {
			t.Fatalf("star %s: %d %s", id, w.Code, w.Body.String())
		}
	}
	// 7 is held by the foreign user too, but only the caller's star counts.
	if w := featureRequest(handler, "DELETE", "/api/favorites/6", "", cookies...); w.Code != 404 {
		t.Fatalf("unstar another user's favorite: %d", w.Code)
	}
	if got := favoriteIDs(getFavorites(t, handler, cookies)); !reflect.DeepEqual(got, []int64{18, 7}) {
		t.Fatalf("%v", got)
	}

	// Reorder: atomic, returns the new list, stale sets are 409.
	w = featureRequest(handler, "PUT", "/api/favorites/order", `{"instrumentIds":[7,18]}`, cookies...)
	var re favoriteList
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &re) != nil || !reflect.DeepEqual(favoriteIDs(re.Favorites), []int64{7, 18}) {
		t.Fatalf("reorder %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{`{"instrumentIds":[7]}`, `{"instrumentIds":[7,18,2]}`, `{"instrumentIds":[7,7]}`, `{"instrumentIds":[7,6]}`, `{"instrumentIds":[]}`} {
		w := featureRequest(handler, "PUT", "/api/favorites/order", body, cookies...)
		if w.Code != 409 || errorCode(t, w.Body.Bytes()) != "FAVORITES_STALE" {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	for _, body := range []string{``, `{}`, `{"instrumentIds":null}`, `{"instrumentIds":"7"}`, `{"instrumentIds":[7,18],"extra":1}`} {
		if w := featureRequest(handler, "PUT", "/api/favorites/order", body, cookies...); w.Code != 400 || errorCode(t, w.Body.Bytes()) != "INVALID_BODY" {
			t.Fatalf("bad body %q: %d %s", body, w.Code, w.Body.String())
		}
	}
	if got := favoriteIDs(getFavorites(t, handler, cookies)); !reflect.DeepEqual(got, []int64{7, 18}) {
		t.Fatalf("a refused reorder changed the order: %v", got)
	}

	// Unstar compacts; a second unstar is a 404.
	if w := featureRequest(handler, "DELETE", "/api/favorites/7", "", cookies...); w.Code != 204 {
		t.Fatalf("unstar: %d", w.Code)
	}
	if w := featureRequest(handler, "DELETE", "/api/favorites/7", "", cookies...); w.Code != 404 {
		t.Fatalf("unstar twice: %d", w.Code)
	}
	list = getFavorites(t, handler, cookies)
	if !reflect.DeepEqual(favoriteIDs(list), []int64{18}) || list[0].Position != 0 {
		t.Fatalf("compacted %+v", list)
	}
	if y.total() != 0 {
		t.Fatal("favorites routes called a provider")
	}
}

func TestFavoriteRoutesCascadeOnItemAndPortfolioDelete(t *testing.T) {
	h, handler, cookies, _, _, _ := yahooPrimary(t)
	a := summaryPortfolio(t, h, "google-user-1", "A", []int64{7, 18}, nil)
	b := summaryPortfolio(t, h, "google-user-1", "B", []int64{18, 2}, nil)
	for _, id := range []int{7, 18, 2} {
		if w := featureRequest(handler, "PUT", fmt.Sprintf("/api/favorites/%d", id), "", cookies...); w.Code != 200 {
			t.Fatalf("star %d: %d", id, w.Code)
		}
	}
	if w := featureRequest(handler, "DELETE", fmt.Sprintf("/api/portfolios/%d/items/7", a), "", cookies...); w.Code != 204 {
		t.Fatalf("remove item: %d", w.Code)
	}
	if got := favoriteIDs(getFavorites(t, handler, cookies)); !reflect.DeepEqual(got, []int64{18, 2}) {
		t.Fatalf("item removed from its last portfolio %v", got)
	}
	if w := featureRequest(handler, "DELETE", fmt.Sprintf("/api/portfolios/%d", a), "", cookies...); w.Code != 204 {
		t.Fatalf("delete portfolio: %d", w.Code)
	}
	if got := favoriteIDs(getFavorites(t, handler, cookies)); !reflect.DeepEqual(got, []int64{18, 2}) {
		t.Fatalf("18 is still held by B %v", got)
	}
	if w := featureRequest(handler, "DELETE", fmt.Sprintf("/api/portfolios/%d", b), "", cookies...); w.Code != 204 {
		t.Fatalf("delete portfolio: %d", w.Code)
	}
	if got := getFavorites(t, handler, cookies); len(got) != 0 {
		t.Fatalf("favorites outlived their portfolios: %+v", got)
	}
}

func TestFavoritesJoinRefreshEstimateStalenessAndJob(t *testing.T) {
	h, handler, cookies, _, _, _ := yahooPrimary(t)
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	h.clock = func() time.Time { return now }
	h.yahoo.Now = h.clock
	summaryPortfolio(t, h, "google-user-1", "Mine", []int64{7, 18, 2}, []int64{7})
	for id, symbol := range map[int64]string{7: "SAP.DE", 18: "VHYD.L"} {
		if err := h.store.LinkYahooSymbol(context.Background(), id, symbol); err != nil {
			t.Fatal(err)
		}
	}
	estimate := func() (refreshInfo, int) {
		info := summaryInfo(t, handler, cookies)
		if info.Estimate == nil {
			t.Fatalf("no estimate %+v", info)
		}
		return info, info.Estimate.Requests
	}
	_, held := estimate()
	if w := featureRequest(handler, "PUT", "/api/favorites/18", "", cookies...); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if _, withFavorite := estimate(); withFavorite != held+1 {
		t.Fatalf("estimate must count the favorite: held %d, with favorite %d", held, withFavorite)
	}
	// A starred title that is not held is the freshness input too: a fresh quote
	// for the held title alone does not make the user's set fresh.
	fresh := func(id int64, at time.Time) {
		q := &marketdata.Quote{InstrumentID: id, Price: "10", OrigPrice: "10", Currency: "EUR", QuoteDate: "2026-11-13", FetchedAt: at, Provider: "YAHOO"}
		if err := h.store.SaveQuote(context.Background(), q, ""); err != nil {
			t.Fatal(err)
		}
	}
	fresh(18, now.Add(-time.Minute))
	if info := summaryInfo(t, handler, cookies); info.Stale || info.NewestFetchedAt == nil || !info.NewestFetchedAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("favorite quote ignored by staleness %+v", info)
	}
	h.clock = func() time.Time { return now.Add(time.Hour) }
	h.yahoo.Now = h.clock
	if info := summaryInfo(t, handler, cookies); !info.Stale || !info.AutoFetch {
		t.Fatalf("expected stale %+v", info)
	}
	result := summaryRefresh(t, handler, cookies, "{}", 202)
	if !result.Started || result.JobID == nil {
		t.Fatalf("%+v", result)
	}
	job := waitPriceJob(t, handler, *result.JobID, cookies)
	ids := []int64{}
	for _, item := range job.Items {
		ids = append(ids, item.InstrumentID)
	}
	if !reflect.DeepEqual(ids, []int64{7, 18}) {
		t.Fatalf("job covers held + favorites only: %v", ids)
	}
}

func TestFavoritesOnlyUserGetsRefreshEstimateAndJob(t *testing.T) {
	h, handler, cookies, _, _, _ := yahooPrimary(t)
	summaryPortfolio(t, h, "google-user-1", "Watch", []int64{7, 18}, nil)
	for id, symbol := range map[int64]string{7: "SAP.DE", 18: "VHYD.L"} {
		if err := h.store.LinkYahooSymbol(context.Background(), id, symbol); err != nil {
			t.Fatal(err)
		}
	}
	if info := summaryInfo(t, handler, cookies); info.Stale || info.AutoFetch || info.Estimate != nil {
		t.Fatalf("nothing held and nothing starred: %+v", info)
	}
	if r := summaryRefresh(t, handler, cookies, "{}", 200); r.Reason != "NOTHING_TO_PRICE" {
		t.Fatalf("%+v", r)
	}
	for _, id := range []string{"7", "18"} {
		if w := featureRequest(handler, "PUT", "/api/favorites/"+id, "", cookies...); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	info := summaryInfo(t, handler, cookies)
	if !info.Stale || !info.AutoFetch || info.Estimate == nil || info.Estimate.Requests != 2 {
		t.Fatalf("favorites-only estimate %+v", info)
	}
	result := summaryRefresh(t, handler, cookies, "{}", 202)
	if !result.Started || result.JobID == nil || result.Requests == nil || *result.Requests != 2 {
		t.Fatalf("%+v", result)
	}
	job := waitPriceJob(t, handler, *result.JobID, cookies)
	ids := []int64{}
	for _, item := range job.Items {
		ids = append(ids, item.InstrumentID)
		if item.Status != "OK" {
			t.Fatalf("item %+v", item)
		}
	}
	if !reflect.DeepEqual(ids, []int64{7, 18}) {
		t.Fatalf("job items %v", ids)
	}
	// The refreshed prices are visible on the favorites straight away.
	for _, f := range getFavorites(t, handler, cookies) {
		if f.PriceEUR == nil || f.AsOf == nil {
			t.Fatalf("favorite not priced after the job: %+v", f)
		}
	}
	// Unstarring everything empties the set again.
	for _, id := range []string{"7", "18"} {
		featureRequest(handler, "DELETE", "/api/favorites/"+id, "", cookies...)
	}
	if r := summaryRefresh(t, handler, cookies, `{"force":true}`, 200); r.Reason != "NOTHING_TO_PRICE" {
		t.Fatalf("%+v", r)
	}
}
