package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"holdings/backend/internal/database"
	"net/http"
	"net/http/httptest"
	"testing"
)

func featureRequest(handler http.Handler, method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}
func TestPortfolioEndpointsRequireSession(t *testing.T) {
	_, handler := auditHandler(t, ":memory:")
	for _, tc := range []struct{ method, path string }{{"GET", "/api/portfolios"}, {"POST", "/api/portfolios"}, {"GET", "/api/portfolios/1"}, {"PATCH", "/api/portfolios/1"}, {"DELETE", "/api/portfolios/1"}, {"POST", "/api/portfolios/1/items"}, {"DELETE", "/api/portfolios/1/items/1"}, {"GET", "/api/instruments/search?q=apple"}} {
		w := featureRequest(handler, tc.method, tc.path, "")
		if w.Code != 401 {
			t.Fatalf("%s %s: %d", tc.method, tc.path, w.Code)
		}
	}
}
func TestPortfolioAPI(t *testing.T) {
	h, handler := auditHandler(t, ":memory:")
	cookies := auditLogin(t, h, handler)
	request := func(method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		w := featureRequest(handler, method, path, body, cookies...)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w
	}
	for _, body := range []string{`{}`, `{"name":"  "}`, `{"name":null}`, `{"name":2}`, `{"name":"a","extra":1}`, `{"name":"a"} {}`} {
		request("POST", "/api/portfolios", body, 400)
	}
	w := request("POST", "/api/portfolios", `{"name":" Growth "}`, 201)
	var p database.Portfolio
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Name != "Growth" {
		t.Fatal(p)
	}
	path := fmt.Sprintf("/api/portfolios/%d", p.ID)
	request("POST", "/api/portfolios", `{"name":"growth"}`, 409)
	request("PATCH", path, `{"name":"Long term"}`, 200)
	request("GET", path, "", 200)
	for _, body := range []string{`{}`, `{"instrumentIds":[]}`, `{"instrumentIds":[999]}`, `{"instrumentIds":[1.5]}`} {
		request("POST", path+"/items", body, 400)
	}
	request("POST", path+"/items", `{"instrumentIds":[1,2]}`, 200)
	w = request("POST", path+"/items", `{"instrumentIds":[1,2]}`, 200)
	var result database.AddedItems
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Added) != 0 || len(result.Skipped) != 2 {
		t.Fatalf("%s %v", w.Body.String(), err)
	}
	request("GET", "/api/instruments/search?q=aap", "", 400)
	request("GET", "/api/instruments/search?q=apple&portfolioId="+fmt.Sprint(p.ID), "", 200)
	request("DELETE", path+"/items/1", "", 204)
	request("DELETE", path+"/items/1", "", 404)
	if _, err := h.store.UpsertGoogleUser(context.Background(), "other", "other@example.com", "", ""); err != nil {
		t.Fatal(err)
	}
	foreign, err := h.store.SavePortfolio(context.Background(), "other", 0, "Private")
	if err != nil {
		t.Fatal(err)
	}
	foreignPath := fmt.Sprintf("/api/portfolios/%d", foreign.ID)
	for _, tc := range []struct{ method, path, body string }{{"GET", foreignPath, ""}, {"PATCH", foreignPath, `{"name":"Stolen"}`}, {"DELETE", foreignPath, ""}, {"POST", foreignPath + "/items", `{"instrumentIds":[1]}`}, {"DELETE", foreignPath + "/items/1", ""}, {"GET", "/api/instruments/search?q=apple&portfolioId=" + fmt.Sprint(foreign.ID), ""}} {
		request(tc.method, tc.path, tc.body, 404)
	}
	w = request("GET", "/api/portfolios", "", 200)
	var list struct {
		Portfolios []database.Portfolio `json:"portfolios"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Portfolios) != 1 {
		t.Fatal(w.Body.String(), err)
	}
	request("DELETE", path, "", 204)
	request("GET", path, "", 404)
}
func TestSignupCapCallback(t *testing.T) {
	h, handler := auditHandler(t, ":memory:")
	cookies := auditLogin(t, h, handler)
	for i := 0; i < 9; i++ {
		if _, err := h.store.UpsertGoogleUser(context.Background(), fmt.Sprint("user", i), "user@example.com", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	auditGoogle(h, `{"sub":"eleventh","email":"new@example.com","email_verified":true}`)
	callback := auditRequest(handler, "GET", "/auth/google/callback?code=valid&state=s", "", &http.Cookie{Name: stateCookie, Value: "s"}, &http.Cookie{Name: verifierCookie, Value: "v"})
	if callback.Code != 302 || callback.Header().Get("Location") != "https://app.example.com/?error=signups_closed" {
		t.Fatalf("%d %s", callback.Code, callback.Header().Get("Location"))
	}
	for _, cookie := range callback.Result().Cookies() {
		if cookie.Name == accessCookie || cookie.Name == refreshCookie {
			t.Fatal("issued session cookie at cap")
		}
	}
	if _, err := h.store.UserByID(context.Background(), "eleventh"); err != database.ErrNotFound {
		t.Fatal("created user at cap", err)
	}
	if w := auditRequest(handler, "GET", "/auth/me", "", cookies...); w.Code != 200 {
		t.Fatal("existing session blocked")
	}
	auditLogin(t, h, handler)
}

func TestTransactionAPI(t *testing.T) {
	h, handler := auditHandler(t, ":memory:")
	cookies := auditLogin(t, h, handler)
	for _, tc := range []struct{ method, path string }{{"GET", "/saving-plan"}, {"GET", "/performance"}, {"GET", "/transactions"}, {"POST", "/transactions"}, {"DELETE", "/transactions/1"}} {
		w := featureRequest(handler, tc.method, "/api/portfolios/1"+tc.path, "")
		if w.Code != 401 {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
	}
	request := func(method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		w := featureRequest(handler, method, path, body, cookies...)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w
	}
	var p database.Portfolio
	w := request("POST", "/api/portfolios", `{"name":"Savings"}`, 201)
	json.Unmarshal(w.Body.Bytes(), &p)
	path := fmt.Sprintf("/api/portfolios/%d", p.ID)
	request("POST", path+"/items", `{"instrumentIds":[1]}`, 200)
	body := `{"date":"2025-01-01","lines":[{"instrumentId":1,"amount":"20.00","price":"10.0000","quantity":"2.000000"}]}`
	w = request("POST", path+"/transactions", body, 201)
	var tx struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &tx)
	w = request("POST", path+"/transactions", body, 409)
	if !bytes.Contains(w.Body.Bytes(), []byte("DUPLICATE_TRANSACTION")) {
		t.Fatal(w.Body.String())
	}
	request("POST", path+"/transactions", `{"date":"2025-01-01","lines":[{"instrumentId":1,"amount":"20,00","price":"10","quantity":"2"}]}`, 400)
	request("POST", path+"/transactions", `{"date":"2025-01-01","lines":[{"instrumentId":1,"amount":20,"price":"10","quantity":"2"}]}`, 400)
	request("GET", path+"/transactions?page=0", "", 400)
	for _, endpoint := range []string{"saving-plan", "transactions", "performance"} {
		request("GET", path+"/"+endpoint, "", 200)
	}
	request("DELETE", path+"/items/1", "", 409)
	h.store.UpsertGoogleUser(context.Background(), "other", "other@example.com", "", "")
	foreign, _ := h.store.SavePortfolio(context.Background(), "other", 0, "Private")
	foreignPath := fmt.Sprintf("/api/portfolios/%d", foreign.ID)
	for _, endpoint := range []string{"saving-plan", "transactions", "performance"} {
		request("GET", foreignPath+"/"+endpoint, "", 404)
	}
	request("POST", foreignPath+"/transactions", body, 404)
	request("DELETE", foreignPath+fmt.Sprintf("/transactions/%d", tx.ID), "", 404)
	own2 := request("POST", "/api/portfolios", `{"name":"Second"}`, 201)
	var second database.Portfolio
	json.Unmarshal(own2.Body.Bytes(), &second)
	request("DELETE", fmt.Sprintf("/api/portfolios/%d/transactions/%d", second.ID, tx.ID), "", 404)
	request("DELETE", path+fmt.Sprintf("/transactions/%d", tx.ID), "", 204)
	request("DELETE", path+"/items/1", "", 204)
}
