// Package catalog enumerates repositories for the /v2/_catalog endpoint
// and the web repository list.
package catalog

import (
	"sort"
	"time"

	"github.com/daqing/airway/lib/repo"
	airwaysql "github.com/daqing/airway/lib/sql"

	"github.com/daqing/air-registry/app/models"
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
// of the repository, falling back to the repository row itself.
func Page(page, perPage int) (repos []RepoSummary, pageCount int, err error) {
	summaries, err := allSummaries()
	if err != nil {
		return nil, 0, err
	}

	total := len(summaries)
	pageCount = max(1, (total+perPage-1)/perPage)
	page = max(1, min(page, pageCount))
	start := (page - 1) * perPage
	return summaries[start:min(start+perPage, total)], pageCount, nil
}

// Recent returns the n most recently active repositories.
func Recent(n int) ([]RepoSummary, error) {
	summaries, err := allSummaries()
	if err != nil {
		return nil, err
	}
	return summaries[:min(n, len(summaries))], nil
}

func allSummaries() ([]RepoSummary, error) {
	all, err := repo.FindAll[models.Repository]()
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
