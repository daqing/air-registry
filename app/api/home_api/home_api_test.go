package home_api

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/daqing/airway/lib/migrate"
	"github.com/daqing/airway/lib/repo"
	airwaysql "github.com/daqing/airway/lib/sql"

	"github.com/daqing/air-registry/app/models"
)

func setupWebServer(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := "sqlite://" + filepath.ToSlash(filepath.Join(t.TempDir(), "registry-web-test.db"))
	if err := migrate.Run(migrate.Options{
		DSN:          dsn,
		Migrations:   os.DirFS(filepath.Join("..", "..", "..", "db", "migrate")),
		SnapshotPath: "",
		Out:          io.Discard,
	}); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if _, err := repo.SetupDB(dsn); err != nil {
		t.Fatalf("setup db: %v", err)
	}

	r := gin.New()
	r.GET("/", IndexAction)
	r.GET("/repos", ReposAction)
	return r
}

// seedRepo creates a repository with one tag, one manifest and one linked
// blob of the given size, with updatedAt as the activity timestamp.
func seedRepo(t *testing.T, name string, size int64, updatedAt time.Time) {
	t.Helper()
	now := updatedAt

	sum := sha256.Sum256([]byte(name))
	id := hex.EncodeToString(sum[:])

	r, err := repo.CreateFrom[models.Repository](airwaysql.H{
		"name":       name,
		"created_at": now,
		"updated_at": now,
	})
	if err != nil {
		t.Fatalf("seed repository: %v", err)
	}

	blob, err := repo.CreateFrom[models.Blob](airwaysql.H{
		"digest":     "sha256:" + id,
		"size":       size,
		"path":       "blobs/sha256/x",
		"created_at": now,
		"updated_at": now,
	})
	if err != nil {
		t.Fatalf("seed blob: %v", err)
	}
	if _, err := repo.CreateFrom[models.RepoBlob](airwaysql.H{
		"repo_id":    r.ID,
		"blob_id":    blob.ID,
		"created_at": now,
		"updated_at": now,
	}); err != nil {
		t.Fatalf("seed repo_blobs: %v", err)
	}

	manifest, err := repo.CreateFrom[models.Manifest](airwaysql.H{
		"repo_id":    r.ID,
		"digest":     "sha256:manifest-" + id,
		"media_type": "application/vnd.oci.image.manifest.v1+json",
		"size":       100,
		"content":    `{"schemaVersion":2}`,
		"created_at": now,
		"updated_at": now,
	})
	if err != nil {
		t.Fatalf("seed manifest: %v", err)
	}
	if _, err := repo.CreateFrom[models.Tag](airwaysql.H{
		"repo_id":         r.ID,
		"name":            "v1",
		"manifest_digest": manifest.Digest,
		"created_at":      now,
		"updated_at":      now,
	}); err != nil {
		t.Fatalf("seed tag: %v", err)
	}
}

func getPage(t *testing.T, r *gin.Engine, url string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	r.ServeHTTP(w, req)
	return w
}

func TestIndexActionRendersHomePage(t *testing.T) {
	r := setupWebServer(t)

	w := getPage(t, r, "/")
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("expected a text/html content type, got %q", ct)
	}

	body := w.Body.String()
	for _, want := range []string{
		"<title>Air Registry</title>",
		`action="/repos"`,
		`name="q"`,
		"No repositories yet",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected body to contain %q, got %q", want, body)
		}
	}
}

func TestIndexActionListsRecentlyPushedFirst(t *testing.T) {
	r := setupWebServer(t)
	seedRepo(t, "demo/old", 100, time.Now().Add(-48*time.Hour))
	seedRepo(t, "demo/new", 2048, time.Now().Add(-time.Hour))

	w := getPage(t, r, "/")
	body := w.Body.String()

	newAt := strings.Index(body, "demo/new")
	oldAt := strings.Index(body, "demo/old")
	if newAt < 0 || oldAt < 0 {
		t.Fatalf("expected both repositories on the home page, got %q", body)
	}
	if newAt > oldAt {
		t.Fatalf("expected demo/new (more recent) before demo/old")
	}
	// Sizes render humanized.
	if !strings.Contains(body, "2.0 KiB") {
		t.Fatalf("expected humanized size, got %q", body)
	}
}

func TestReposActionEmptyState(t *testing.T) {
	r := setupWebServer(t)

	w := getPage(t, r, "/repos")
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `class="aw-empty"`) {
		t.Fatalf("expected an empty state, got %q", body)
	}
	// One page: no pagination island.
	if strings.Contains(body, "data-island") {
		t.Fatalf("expected no pagination island for a single page")
	}
}

func TestReposActionListsRepositories(t *testing.T) {
	r := setupWebServer(t)
	seedRepo(t, "demo/app", 500, time.Now().Add(-2*time.Hour))
	seedRepo(t, "library/nginx", 1500, time.Now().Add(-time.Hour))

	w := getPage(t, r, "/repos")
	body := w.Body.String()

	if !strings.Contains(body, "demo/app") || !strings.Contains(body, "library/nginx") {
		t.Fatalf("expected both repositories listed, got %q", body)
	}
	if strings.Contains(body, `class="aw-empty"`) {
		t.Fatalf("did not expect an empty state")
	}
	// nginx is more recent: it comes first.
	if strings.Index(body, "library/nginx") > strings.Index(body, "demo/app") {
		t.Fatalf("expected library/nginx (more recent) first")
	}
}

func TestReposActionPagination(t *testing.T) {
	r := setupWebServer(t)
	for i := 0; i < 12; i++ {
		name := "repo/" + string(rune('a'+i)) + "repo"
		seedRepo(t, name, 100, time.Now().Add(time.Duration(-i)*time.Hour))
	}

	w := getPage(t, r, "/repos")
	body := w.Body.String()
	if !strings.Contains(body, `data-island="pagination"`) {
		t.Fatalf("expected the pagination island on page 1")
	}
	if !strings.Contains(body, `"pageCount":2`) {
		t.Fatalf("expected pageCount 2 in island props, got %q", body)
	}
	if !strings.Contains(body, "repo/arepo") || !strings.Contains(body, "repo/jrepo") {
		t.Fatalf("expected the first 10 repositories on page 1")
	}
	if strings.Contains(body, "repo/krepo") {
		t.Fatalf("did not expect page 2 repositories on page 1")
	}

	w = getPage(t, r, "/repos?page=2")
	body = w.Body.String()
	if !strings.Contains(body, "repo/krepo") || !strings.Contains(body, "repo/lrepo") {
		t.Fatalf("expected the remaining 2 repositories on page 2, got %q", body)
	}

	// Out-of-range pages clamp to the last page.
	w = getPage(t, r, "/repos?page=99")
	if !strings.Contains(w.Body.String(), "repo/krepo") {
		t.Fatalf("expected page=99 to clamp to the last page")
	}
	// Invalid page values fall back to page 1.
	w = getPage(t, r, "/repos?page=abc")
	if !strings.Contains(w.Body.String(), "repo/arepo") {
		t.Fatalf("expected page=abc to fall back to page 1")
	}
}

func TestReposActionKeepsQueryInPaginationBase(t *testing.T) {
	r := setupWebServer(t)
	for i := 0; i < 12; i++ {
		seedRepo(t, "repo/q"+string(rune('a'+i)), 100, time.Now().Add(time.Duration(-i)*time.Hour))
	}

	w := getPage(t, r, "/repos?q=foo")
	if !strings.Contains(w.Body.String(), `"base":"/repos?q=foo"`) {
		t.Fatalf("expected q preserved in the pagination base, got %q", w.Body.String())
	}
}
