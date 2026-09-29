// Package catalog enumerates repositories for the /v2/_catalog endpoint.
package catalog

import (
	"github.com/daqing/airway/lib/repo"
	airwaysql "github.com/daqing/airway/lib/sql"

	"github.com/daqing/air-registry/app/models"
)

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
