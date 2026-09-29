package home_api

import (
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/daqing/airway/lib/render"
	"github.com/daqing/airway/lib/utils"

	"github.com/daqing/air-registry/app/services/catalog"
	"github.com/daqing/air-registry/app/services/manifests"
	"github.com/daqing/air-registry/app/views/home"
	"github.com/daqing/air-registry/app/views/repos"
)

const (
	reposPageSize = 10
	recentCount   = 5
)

// IndexAction renders the home page with the most recently pushed
// repositories.
func IndexAction(c *gin.Context) {
	recent, err := catalog.Recent(recentCount)
	if err != nil {
		log.Printf("home: list recent repositories: %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}
	render.HTML(c, home.Index(recent))
}

// ReposAction renders the paginated repository list. ?page= selects the
// page (1-based, clamped); ?q= filters repositories by name (substring).
func ReposAction(c *gin.Context) {
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 {
		page = 1
	}

	q := c.Query("q")
	repoPage, pageCount, err := catalog.Page(page, reposPageSize, q)
	if err != nil {
		log.Printf("repos: list repositories: %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}

	// The pagination island starts from the current URL without its page
	// parameter, so other query args (e.g. q) survive page changes.
	base := utils.URLPrefix() + "/repos"
	if q := c.Query("q"); q != "" {
		base += "?q=" + url.QueryEscape(q)
	}

	render.HTML(c, repos.List(repos.ListProps{
		Repos:     repoPage,
		Page:      page,
		PageCount: pageCount,
		Query:     q,
		Base:      base,
	}))
}

// RepoAction renders the repository detail page: the tag table, plus the
// expanded manifest when ?tag= or ?digest= selects one (the two are how the
// page drills into a tag or a referrer). Repository names have multiple
// segments, so the route is a *path wildcard whose value is the raw name.
func RepoAction(c *gin.Context) {
	name := strings.TrimPrefix(c.Param("path"), "/")
	if name == "" {
		c.Status(http.StatusNotFound)
		return
	}

	d, err := catalog.Detail(name)
	if err != nil {
		log.Printf("repos: detail %q: %v", name, err)
		c.Status(http.StatusInternalServerError)
		return
	}
	if d == nil {
		c.Status(http.StatusNotFound)
		return
	}

	props := repos.DetailProps{Repo: d}
	status := http.StatusOK

	ref := c.Query("digest")
	if ref == "" {
		ref = c.Query("tag")
	}
	if ref != "" {
		m, err := manifests.Find(name, ref)
		if err != nil {
			log.Printf("repos: find manifest %q %q: %v", name, ref, err)
			c.Status(http.StatusInternalServerError)
			return
		}
		if m == nil {
			props.Notice = "No manifest named " + ref
			status = http.StatusNotFound
		} else {
			expanded, err := manifests.Expand(m.RepoID, m)
			if err != nil {
				log.Printf("repos: expand manifest %q %q: %v", name, ref, err)
				c.Status(http.StatusInternalServerError)
				return
			}
			referrers, err := manifests.Referrers(name, m.Digest, "")
			if err != nil {
				log.Printf("repos: referrers %q %q: %v", name, ref, err)
				c.Status(http.StatusInternalServerError)
				return
			}
			props.Manifest = expanded
			props.Referrers = referrers
		}
	}

	render.HTMLStatus(c, status, repos.Detail(props))
}
