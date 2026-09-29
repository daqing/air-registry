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
	r.GET("/repos/*path", RepoAction)
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

// dgst builds a syntactically valid digest from a repeated hex char.
func dgst(hex string) string {
	return "sha256:" + strings.Repeat(hex, 32)
}

func ensureRepo(t *testing.T, name string, now time.Time) *models.Repository {
	t.Helper()
	r, err := repo.FindOneBy[models.Repository](airwaysql.H{"name": name})
	if err != nil {
		t.Fatalf("find repository: %v", err)
	}
	if r != nil {
		return r
	}
	r, err = repo.CreateFrom[models.Repository](airwaysql.H{
		"name":       name,
		"created_at": now,
		"updated_at": now,
	})
	if err != nil {
		t.Fatalf("create repository: %v", err)
	}
	return r
}

func seedManifestRow(t *testing.T, r *models.Repository, digest, mediaType, content string, artifactType, subject *string, now time.Time) {
	t.Helper()
	if _, err := repo.CreateFrom[models.Manifest](airwaysql.H{
		"repo_id":        r.ID,
		"digest":         digest,
		"media_type":     mediaType,
		"artifact_type":  artifactType,
		"subject_digest": subject,
		"size":           int64(len(content)),
		"content":        content,
		"created_at":     now,
		"updated_at":     now,
	}); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}
}

func seedTagRow(t *testing.T, r *models.Repository, name, digest string, now time.Time) {
	t.Helper()
	if _, err := repo.CreateFrom[models.Tag](airwaysql.H{
		"repo_id":         r.ID,
		"name":            name,
		"manifest_digest": digest,
		"created_at":      now,
		"updated_at":      now,
	}); err != nil {
		t.Fatalf("seed tag: %v", err)
	}
}

// seedImageRepo seeds a repository with an OCI image manifest (config 100 B,
// layers 500 B + 700 B, annotations) tagged v1 and latest.
func seedImageRepo(t *testing.T, repoName string, now time.Time) (imageDigest string) {
	t.Helper()
	r := ensureRepo(t, repoName, now)
	imageDigest = dgst("dd")
	content := `{
		"schemaVersion": 2,
		"mediaType": "application/vnd.oci.image.manifest.v1+json",
		"config": {"mediaType": "application/vnd.oci.image.config.v1+json", "digest": "` + dgst("11") + `", "size": 100},
		"layers": [
			{"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip", "digest": "` + dgst("22") + `", "size": 500},
			{"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip", "digest": "` + dgst("33") + `", "size": 700}
		],
		"annotations": {"org.opencontainers.image.title": "demo app"}
	}`
	seedManifestRow(t, r, imageDigest, "application/vnd.oci.image.manifest.v1+json", content, nil, nil, now)
	seedTagRow(t, r, "v1", imageDigest, now)
	seedTagRow(t, r, "latest", imageDigest, now.Add(-time.Hour))
	return imageDigest
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

	w := getPage(t, r, "/repos?q=repo")
	if !strings.Contains(w.Body.String(), `"base":"/repos?q=repo"`) {
		t.Fatalf("expected q preserved in the pagination base, got %q", w.Body.String())
	}
}

func TestReposActionSearchFiltersByName(t *testing.T) {
	r := setupWebServer(t)
	seedRepo(t, "demo/app", 100, time.Now().Add(-2*time.Hour))
	seedRepo(t, "library/nginx", 200, time.Now().Add(-time.Hour))

	w := getPage(t, r, "/repos?q=nginx")
	body := w.Body.String()
	if !strings.Contains(body, "library/nginx") {
		t.Fatalf("expected library/nginx in the search results, got %q", body)
	}
	if strings.Contains(body, "demo/app") {
		t.Fatalf("did not expect demo/app in the search results, got %q", body)
	}
	if !strings.Contains(body, "Results for") {
		t.Fatalf("expected a results caption for the query, got %q", body)
	}
}

func TestReposActionSearchEscapesLikeWildcards(t *testing.T) {
	r := setupWebServer(t)
	seedRepo(t, "demo/app", 100, time.Now().Add(-2*time.Hour))
	seedRepo(t, "demo/100%", 100, time.Now().Add(-3*time.Hour))
	seedRepo(t, "under_score/x", 100, time.Now().Add(-4*time.Hour))

	// '%' is a LIKE wildcard; it must match a literal '%' only.
	w := getPage(t, r, "/repos?q=%25") // %25 → '%'
	body := w.Body.String()
	if !strings.Contains(body, "demo/100%") || strings.Contains(body, "demo/app") {
		t.Fatalf("expected only the literal %q match, got %q", "%", body)
	}

	// Same for '_'.
	w = getPage(t, r, "/repos?q=_")
	body = w.Body.String()
	if !strings.Contains(body, "under_score/x") || strings.Contains(body, "demo/app") {
		t.Fatalf("expected only the literal _ match, got %q", body)
	}

	// Backslash and quote must not break the query.
	w = getPage(t, r, "/repos?q=%5C") // %5C → '\'
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 for a backslash query, got %d", w.Code)
	}
	w = getPage(t, r, "/repos?q=app%27") // app'
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 for a quoted query, got %d", w.Code)
	}
}

func TestReposActionSearchNoResultsShowsClearSearch(t *testing.T) {
	r := setupWebServer(t)
	seedRepo(t, "demo/app", 100, time.Now().Add(-time.Hour))

	w := getPage(t, r, "/repos?q=nomatch")
	body := w.Body.String()
	if !strings.Contains(body, `class="aw-empty"`) {
		t.Fatalf("expected an empty state, got %q", body)
	}
	if !strings.Contains(body, "No repositories match") {
		t.Fatalf("expected a no-results message, got %q", body)
	}
	if !strings.Contains(body, "Clear search") || !strings.Contains(body, `href="/repos"`) {
		t.Fatalf("expected a clear-search entry, got %q", body)
	}
}

func TestRepoActionRendersTagTable(t *testing.T) {
	r := setupWebServer(t)
	now := time.Now()
	imageDigest := seedImageRepo(t, "demo/app", now)

	// A second manifest with its own tag; v2 shares the image manifest.
	repo := ensureRepo(t, "demo/app", now)
	other := dgst("ee")
	seedManifestRow(t, repo, other,
		"application/vnd.oci.image.manifest.v1+json",
		`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","layers":[]}`,
		nil, nil, now.Add(-30*time.Minute))
	seedTagRow(t, repo, "v2", imageDigest, now.Add(-time.Minute))
	seedTagRow(t, repo, "edge", other, now.Add(-30*time.Minute))

	w := getPage(t, r, "/repos/demo/app")
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	body := w.Body.String()

	for _, want := range []string{
		"demo/app",
		`href="/repos/demo/app?tag=v1"`,
		`href="/repos/demo/app?tag=v2"`,
		`href="/repos/demo/app?tag=edge"`,
		// Image manifest total: 100 + 500 + 700 = 1300 → 1.3 KiB.
		"1.3 KiB",
		// The layer-less manifest totals 0.
		"0 B",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected body to contain %q, got %q", want, body)
		}
	}
	// Sorted by tag name: edge, latest, v1, v2.
	if strings.Index(body, "?tag=edge") > strings.Index(body, "?tag=latest") ||
		strings.Index(body, "?tag=latest") > strings.Index(body, "?tag=v1") {
		t.Fatalf("expected tags sorted by name, got %q", body)
	}
}

func TestRepoActionExpandsManifestByTag(t *testing.T) {
	r := setupWebServer(t)
	imageDigest := seedImageRepo(t, "demo/app", time.Now())

	w := getPage(t, r, "/repos/demo/app?tag=v1")
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	body := w.Body.String()

	for _, want := range []string{
		imageDigest, // full digest of the selected manifest
		"application/vnd.oci.image.manifest.v1+json",
		"Total size",
		"1.3 KiB",
		// Config row.
		"sha256:" + strings.Repeat("11", 12),
		// Layer rows: digests and humanized sizes.
		"sha256:" + strings.Repeat("22", 12),
		"sha256:" + strings.Repeat("33", 12),
		"500 B",
		"700 B",
		// Annotations.
		"org.opencontainers.image.title",
		"demo app",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected body to contain %q, got %q", want, body)
		}
	}
}

func TestRepoActionExpandsIndexByTag(t *testing.T) {
	r := setupWebServer(t)
	now := time.Now()
	repo := ensureRepo(t, "multi/arch", now)

	childContent := `{
		"schemaVersion": 2,
		"mediaType": "application/vnd.oci.image.manifest.v1+json",
		"config": {"mediaType": "application/vnd.oci.image.config.v1+json", "digest": "` + dgst("44") + `", "size": 80},
		"layers": [{"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip", "digest": "` + dgst("55") + `", "size": 400}]
	}`
	seedManifestRow(t, repo, dgst("aa"), "application/vnd.oci.image.manifest.v1+json", childContent, nil, nil, now)
	seedManifestRow(t, repo, dgst("bb"), "application/vnd.oci.image.manifest.v1+json", childContent, nil, nil, now)

	indexContent := `{
		"schemaVersion": 2,
		"mediaType": "application/vnd.oci.image.index.v1+json",
		"manifests": [
			{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": "` + dgst("aa") + `", "size": 480, "platform": {"architecture": "amd64", "os": "linux"}},
			{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": "` + dgst("bb") + `", "size": 480, "platform": {"architecture": "arm64", "os": "linux", "variant": "v8"}}
		]
	}`
	seedManifestRow(t, repo, dgst("99"), "application/vnd.oci.image.index.v1+json", indexContent, nil, nil, now)
	seedTagRow(t, repo, "latest", dgst("99"), now)

	w := getPage(t, r, "/repos/multi/arch?tag=latest")
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	body := w.Body.String()

	for _, want := range []string{
		dgst("99"),
		"application/vnd.oci.image.index.v1+json",
		"linux/amd64",
		"linux/arm64/v8",
		"sha256:" + strings.Repeat("aa", 12),
		"sha256:" + strings.Repeat("bb", 12),
		// Total: children 480×2 + their config+layers 480×2 = 1920 → 1.9 KiB.
		"1.9 KiB",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected body to contain %q, got %q", want, body)
		}
	}
}

func TestRepoActionListsReferrers(t *testing.T) {
	r := setupWebServer(t)
	now := time.Now()
	imageDigest := seedImageRepo(t, "demo/app", now)

	repo := ensureRepo(t, "demo/app", now)
	artifactType := "application/vnd.example.sbom"
	sbomDigest := dgst("cc")
	sbomContent := `{
		"schemaVersion": 2,
		"mediaType": "application/vnd.oci.image.manifest.v1+json",
		"artifactType": "` + artifactType + `",
		"config": {"mediaType": "application/vnd.oci.empty.v1+json", "digest": "` + dgst("44") + `", "size": 2},
		"layers": [{"mediaType": "application/json", "digest": "` + dgst("55") + `", "size": 300}],
		"subject": {"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": "` + imageDigest + `"}
	}`
	seedManifestRow(t, repo, sbomDigest, "application/vnd.oci.image.manifest.v1+json", sbomContent, &artifactType, &imageDigest, now.Add(time.Minute))

	// The selected image lists its referrer.
	w := getPage(t, r, "/repos/demo/app?tag=v1")
	body := w.Body.String()
	if !strings.Contains(body, "Referrers") || !strings.Contains(body, artifactType) {
		t.Fatalf("expected the referrers table, got %q", body)
	}
	if !strings.Contains(body, `href="/repos/demo/app?digest=sha256%3A`+strings.Repeat("cc", 32)+`"`) {
		t.Fatalf("expected a link to the referrer manifest, got %q", body)
	}

	// The referrer itself links back to its subject.
	w = getPage(t, r, "/repos/demo/app?digest="+sbomDigest)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	body = w.Body.String()
	if !strings.Contains(body, "Subject") || !strings.Contains(body, imageDigest) {
		t.Fatalf("expected a subject link back to the image, got %q", body)
	}
}

func TestRepoActionUnknownReferences(t *testing.T) {
	r := setupWebServer(t)
	imageDigest := seedImageRepo(t, "demo/app", time.Now())

	w := getPage(t, r, "/repos/no/such/repo")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 for an unknown repository, got %d", w.Code)
	}

	// Unknown tag: 404, a notice, but the repository tag table still renders.
	w = getPage(t, r, "/repos/demo/app?tag=v9")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 for an unknown tag, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "No manifest named v9") {
		t.Fatalf("expected a notice for the unknown tag, got %q", body)
	}
	if !strings.Contains(body, `href="/repos/demo/app?tag=v1"`) || !strings.Contains(body, imageDigest) {
		t.Fatalf("expected the tag table to still render, got %q", body)
	}
}
