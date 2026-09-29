package v2_api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/daqing/air-registry/app/services/blobs"
)

type catalogResponse struct {
	Repositories []string `json:"repositories"`
}

func seedRepos(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := blobs.EnsureRepository(name); err != nil {
			t.Fatalf("seed repository %q: %v", name, err)
		}
	}
}

func getCatalog(t *testing.T, r *gin.Engine, query string) *httptest.ResponseRecorder {
	t.Helper()
	w := uploadRequest(t, r, http.MethodGet, "/v2/_catalog"+query, nil)
	return w
}

func decodeCatalog(t *testing.T, w *httptest.ResponseRecorder) catalogResponse {
	t.Helper()
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("expected JSON content type, got %q", ct)
	}
	var resp catalogResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode catalog response: %v (%s)", err, w.Body.String())
	}
	if resp.Repositories == nil {
		t.Fatalf("repositories must be [] not null (%s)", w.Body.String())
	}
	return resp
}

func TestCatalogEmptyRegistry(t *testing.T) {
	r, _ := setupUploadServer(t)

	w := getCatalog(t, r, "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	resp := decodeCatalog(t, w)
	if len(resp.Repositories) != 0 {
		t.Fatalf("expected empty catalog, got %v", resp.Repositories)
	}
}

func TestCatalogListsAllSorted(t *testing.T) {
	r, _ := setupUploadServer(t)
	seedRepos(t, "zebra", "demo/app", "library/nginx", "demo/base")

	w := getCatalog(t, r, "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	resp := decodeCatalog(t, w)
	want := []string{"demo/app", "demo/base", "library/nginx", "zebra"}
	if len(resp.Repositories) != len(want) {
		t.Fatalf("expected %v, got %v", want, resp.Repositories)
	}
	for i := range want {
		if resp.Repositories[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, resp.Repositories)
		}
	}
	if link := w.Header().Get("Link"); link != "" {
		t.Fatalf("expected no Link header without n, got %q", link)
	}
}

func TestCatalogPaginationFollowsLink(t *testing.T) {
	r, _ := setupUploadServer(t)
	seedRepos(t, "repo/a", "repo/b", "repo/c", "repo/d", "repo/e")

	// Follow the Link header like a client would.
	next := "?n=2"
	var pages [][]string
	for i := 0; i < 3; i++ {
		w := getCatalog(t, r, next)
		if w.Code != http.StatusOK {
			t.Fatalf("page %d: expected 200, got %d (%s)", i, w.Code, w.Body.String())
		}
		pages = append(pages, decodeCatalog(t, w).Repositories)

		link := w.Header().Get("Link")
		if link == "" {
			next = ""
			continue
		}
		start, end := strings.Index(link, "<"), strings.Index(link, ">")
		if start < 0 || end <= start {
			t.Fatalf("page %d: malformed Link header %q", i, link)
		}
		next = strings.TrimPrefix(link[start+1:end], "/v2/_catalog")
	}

	want := [][]string{{"repo/a", "repo/b"}, {"repo/c", "repo/d"}, {"repo/e"}}
	for i := range want {
		if len(pages[i]) != len(want[i]) {
			t.Fatalf("page %d: expected %v, got %v", i, want[i], pages[i])
		}
		for j := range want[i] {
			if pages[i][j] != want[i][j] {
				t.Fatalf("page %d: expected %v, got %v", i, want[i], pages[i])
			}
		}
	}

	if len(pages[0]) == 0 || len(pages[1]) == 0 || len(pages[2]) == 0 {
		t.Fatalf("expected three non-empty pages, got %v", pages)
	}
	// First two pages carry a next Link; the last does not.
	w1 := getCatalog(t, r, "?n=2")
	if l := w1.Header().Get("Link"); l != `</v2/_catalog?n=2&last=repo/b>; rel="next"` {
		t.Fatalf("unexpected first Link header %q", l)
	}
	w2 := getCatalog(t, r, "?n=2&last=repo/b")
	if l := w2.Header().Get("Link"); l != `</v2/_catalog?n=2&last=repo/d>; rel="next"` {
		t.Fatalf("unexpected second Link header %q", l)
	}
	w3 := getCatalog(t, r, "?n=2&last=repo/d")
	if l := w3.Header().Get("Link"); l != "" {
		t.Fatalf("expected no Link on last page, got %q", l)
	}
}

func TestCatalogLastWithoutN(t *testing.T) {
	r, _ := setupUploadServer(t)
	seedRepos(t, "repo/a", "repo/b", "repo/c", "repo/d")

	w := getCatalog(t, r, "?last=repo/b")
	resp := decodeCatalog(t, w)
	want := []string{"repo/c", "repo/d"}
	if len(resp.Repositories) != len(want) {
		t.Fatalf("expected %v, got %v", want, resp.Repositories)
	}
	for i := range want {
		if resp.Repositories[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, resp.Repositories)
		}
	}
}

func TestCatalogLastBeyondEnd(t *testing.T) {
	r, _ := setupUploadServer(t)
	seedRepos(t, "repo/a")

	w := getCatalog(t, r, "?last=zzz")
	resp := decodeCatalog(t, w)
	if len(resp.Repositories) != 0 {
		t.Fatalf("expected empty page, got %v", resp.Repositories)
	}
}

func TestCatalogInvalidN(t *testing.T) {
	r, _ := setupUploadServer(t)
	seedRepos(t, "repo/a")

	for _, query := range []string{"?n=abc", "?n=-1", "?n=0"} {
		w := getCatalog(t, r, query)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", query, w.Code)
		}
	}
}

func TestCatalogRejectsNonGet(t *testing.T) {
	r, _ := setupUploadServer(t)

	w := uploadRequest(t, r, http.MethodPost, "/v2/_catalog", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
