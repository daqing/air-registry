package home_api

import (
	"log"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/daqing/airway/lib/render"
	"github.com/daqing/airway/lib/utils"

	"github.com/daqing/air-registry/app/services/catalog"
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
