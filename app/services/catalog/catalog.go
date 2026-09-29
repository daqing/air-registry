// Package catalog enumerates repositories for the /v2/_catalog endpoint
// and the web repository list.
package catalog

import (
	"sort"
	"strings"
	"time"

	"github.com/daqing/airway/lib/repo"
	airwaysql "github.com/daqing/airway/lib/sql"

	"github.com/daqing/air-registry/app/models"
	"github.com/daqing/air-registry/app/services/manifests"
)

// RepoSummary is one row of the web repository list.
type RepoSummary struct {
	Name      string    `db:"name" json:"name"`
	TagCount  int64     `db:"tag_count" json:"tag_count"`
	TotalSize int64     `db:"total_size" json:"total_size"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// List returns repository names ordered lexicographically, starting after
// last (exclusive) when last is non-empty, capped at n names when n > 0.
// It reports hasMore when a following page exists.
func List(last string, n int) (names []string, hasMore bool, err error) {
	b := airwaysql.Select("name").
		FromTable(airwaysql.TableFor(models.Repository{})).
		OrderBy("name ASC")
	if last != "" {
		b = b.Where(airwaysql.Gt("name", last))
	}

	limit := n
	if limit > 0 {
		limit++ // peek one extra row to detect the following page
		b = b.Limit(limit)
	}

	rows, err := repo.Find[models.Repository](repo.CurrentDB(), b)
	if err != nil {
		return nil, false, err
	}

	names = make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.Name)
	}

	if n > 0 && len(names) > n {
		return names[:n], true, nil
	}
	return names, false, nil
}

// Page returns the page-th (1-based) chunk of repositories ordered by most
// recent activity, plus the total page count. Out-of-range pages clamp to
// the first/last page. Activity is derived from the newest tag or manifest
// of the repository, falling back to the repository row itself. When q is
// non-empty, only repositories whose name contains q are returned (LIKE,
// wildcards in q escaped).
func Page(page, perPage int, q string) (repos []RepoSummary, pageCount int, err error) {
	summaries, err := allSummaries(q)
	if err != nil {
		return nil, 0, err
	}

	total := len(summaries)
	pageCount = max(1, (total+perPage-1)/perPage)
	page = max(1, min(page, pageCount))
	start := (page - 1) * perPage
	return summaries[start:min(start+perPage, total)], pageCount, nil
}

// TagSummary is one row of the repository detail tag table.
type TagSummary struct {
	Name      string
	Digest    string
	Size      int64
	UpdatedAt time.Time
}

// RepoDetail is what the repository detail page shows: the tag table.
type RepoDetail struct {
	Name string
	Tags []TagSummary
}

// Detail aggregates the repository detail page data. Manifest sizes come
// from manifests.Expand, memoized per digest so tags sharing a manifest are
// expanded once. Returns (nil, nil) when the repository does not exist.
func Detail(repoName string) (*RepoDetail, error) {
	r, err := repo.FindOneBy[models.Repository](airwaysql.H{"name": repoName})
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, nil
	}

	tags, err := repo.FindBy[models.Tag](airwaysql.H{"repo_id": r.ID})
	if err != nil {
		return nil, err
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i].Name < tags[j].Name })

	d := &RepoDetail{Name: r.Name, Tags: make([]TagSummary, 0, len(tags))}
	sizes := map[string]int64{}
	for _, t := range tags {
		size, ok := sizes[t.ManifestDigest]
		if !ok {
			m, err := repo.FindOneBy[models.Manifest](airwaysql.H{
				"repo_id": r.ID,
				"digest":  t.ManifestDigest,
			})
			if err != nil {
				return nil, err
			}
			if m != nil {
				expanded, err := manifests.Expand(r.ID, m)
				if err != nil {
					return nil, err
				}
				size = expanded.TotalSize
			}
			sizes[t.ManifestDigest] = size
		}
		d.Tags = append(d.Tags, TagSummary{
			Name:      t.Name,
			Digest:    t.ManifestDigest,
			Size:      size,
			UpdatedAt: t.UpdatedAt,
		})
	}
	return d, nil
}

// Recent returns the n most recently active repositories.
func Recent(n int) ([]RepoSummary, error) {
	summaries, err := allSummaries("")
	if err != nil {
		return nil, err
	}
	return summaries[:min(n, len(summaries))], nil
}

func allSummaries(q string) ([]RepoSummary, error) {
	b := airwaysql.Select("*").
		FromTable(airwaysql.TableFor(models.Repository{}))
	if q != "" {
		b = b.Where(nameMatches(q))
	}
	all, err := repo.Find[models.Repository](repo.CurrentDB(), b)
	if err != nil {
		return nil, err
	}

	summaries := make([]RepoSummary, 0, len(all))
	for _, r := range all {
		s := RepoSummary{Name: r.Name, UpdatedAt: r.UpdatedAt}

		tags, err := repo.FindBy[models.Tag](airwaysql.H{"repo_id": r.ID})
		if err != nil {
			return nil, err
		}
		s.TagCount = int64(len(tags))
		for _, t := range tags {
			if t.UpdatedAt.After(s.UpdatedAt) {
				s.UpdatedAt = t.UpdatedAt
			}
		}

		manifests, err := repo.FindBy[models.Manifest](airwaysql.H{"repo_id": r.ID})
		if err != nil {
			return nil, err
		}
		for _, m := range manifests {
			if m.UpdatedAt.After(s.UpdatedAt) {
				s.UpdatedAt = m.UpdatedAt
			}
		}

		size, err := linkedBlobSize(r.ID)
		if err != nil {
			return nil, err
		}
		s.TotalSize = size

		summaries = append(summaries, s)
	}

	sort.SliceStable(summaries, func(i, j int) bool {
		return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
	})
	return summaries, nil
}

// nameMatches builds a substring match on the repository name, treating the
// user's q literally by escaping the LIKE wildcards.
func nameMatches(q string) airwaysql.CondBuilder {
	return airwaysql.RawCondition(
		`name LIKE @name_pattern ESCAPE '\'`,
		airwaysql.NamedArgs{"name_pattern": "%" + escapeLike(q) + "%"},
	)
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

func linkedBlobSize(repoID airwaysql.IdType) (int64, error) {
	links, err := repo.FindBy[models.RepoBlob](airwaysql.H{"repo_id": repoID})
	if err != nil || len(links) == 0 {
		return 0, err
	}

	ids := make([]airwaysql.IdType, 0, len(links))
	for _, l := range links {
		ids = append(ids, l.BlobID)
	}
	blobs, err := repo.Find[models.Blob](repo.CurrentDB(),
		airwaysql.Select("*").
			FromTable(airwaysql.TableFor(models.Blob{})).
			Where(airwaysql.In("id", ids)))
	if err != nil {
		return 0, err
	}

	var size int64
	for _, b := range blobs {
		size += b.Size
	}
	return size, nil
}
