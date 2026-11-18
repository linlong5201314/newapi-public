package model

import (
	_ "embed"
	"regexp"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

//go:embed magpie_plugins.json
var magpiePluginsJSON []byte

// MagpiePlugin is a catalog entry, not a provisioned or authenticated provider.
// Empty Install/ImportSource means no unambiguous install source was verified.
type MagpiePlugin struct {
	Id           string `json:"id"`
	Name         string `json:"name"`
	Repo         string `json:"repo"`
	Desc         string `json:"desc"`
	AuthMode     string `json:"authMode"`
	Stars        int    `json:"stars"`
	Url          string `json:"url"`
	Install      string `json:"install"`
	ImportSource string `json:"importSource"`
}

var magpieGitHubRepo = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)
var magpieScopedPackage = regexp.MustCompile(`^@[a-z0-9][a-z0-9._-]*/[a-z0-9][a-z0-9._-]*$`)

// The upstream CLI documents github:owner/repo and scoped npm packages. The
// graphical plugin importer accepts a GitHub URL or the scoped package name.
// Do not guess an unscoped npm package from an owner-less repository label.
var loadMagpiePlugins = sync.OnceValue(func() []MagpiePlugin {
	var rows []MagpiePlugin
	if err := common.Unmarshal(magpiePluginsJSON, &rows); err != nil {
		common.SysError("Failed to decode embedded Magpie plugin catalog")
		return []MagpiePlugin{}
	}
	for i := range rows {
		rows[i].Install, rows[i].ImportSource, rows[i].Url = "", "", ""
		switch {
		case magpieScopedPackage.MatchString(rows[i].Repo):
			rows[i].Url = "https://www.npmjs.com/package/" + rows[i].Repo
			rows[i].ImportSource = rows[i].Repo
			rows[i].Install = "magpie plugin add " + rows[i].Repo
		case magpieGitHubRepo.MatchString(rows[i].Repo):
			rows[i].Url = "https://github.com/" + rows[i].Repo
			rows[i].ImportSource = rows[i].Url
			rows[i].Install = "magpie plugin add github:" + rows[i].Repo
		}
	}
	return rows
})

// ListMagpiePlugins returns a copy so callers cannot mutate the catalog cache.
func ListMagpiePlugins(keyword string) []MagpiePlugin {
	all := loadMagpiePlugins()
	kw := strings.ToLower(strings.TrimSpace(keyword))
	out := make([]MagpiePlugin, 0, len(all))
	for _, p := range all {
		if kw == "" || strings.Contains(strings.ToLower(p.Name), kw) ||
			strings.Contains(strings.ToLower(p.Repo), kw) ||
			strings.Contains(strings.ToLower(p.Desc), kw) ||
			strings.Contains(strings.ToLower(p.Id), kw) {
			out = append(out, p)
		}
	}
	return out
}
