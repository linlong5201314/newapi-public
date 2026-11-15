package model

import (
	_ "embed"
	"encoding/json"
	"strings"
)

// magpiePluginsJSON 是精选的 magpie 插件目录（来自 GitHub topic: magpie-plugin）。
//
//go:embed magpie_plugins.json
var magpiePluginsJSON []byte

// MagpiePlugin 描述一个可接入的 magpie provider 插件。
type MagpiePlugin struct {
	Id       string `json:"id"`
	Name     string `json:"name"`
	Repo     string `json:"repo"`
	Desc     string `json:"desc"`
	AuthMode string `json:"authMode"`
	Stars    int    `json:"stars"`
	Url      string `json:"url"`
	Install  string `json:"install"`
}

var magpiePluginCache []MagpiePlugin

func loadMagpiePlugins() []MagpiePlugin {
	if magpiePluginCache != nil {
		return magpiePluginCache
	}
	var rows []MagpiePlugin
	if err := json.Unmarshal(magpiePluginsJSON, &rows); err != nil {
		return nil
	}
	for i := range rows {
		rows[i].Url = "https://github.com/" + rows[i].Repo
		rows[i].Install = "magpie plugin add " + rows[i].Repo
	}
	magpiePluginCache = rows
	return rows
}

// ListMagpiePlugins 支持按名称/仓库/描述做大小写不敏感过滤。
func ListMagpiePlugins(keyword string) []MagpiePlugin {
	all := loadMagpiePlugins()
	kw := strings.ToLower(strings.TrimSpace(keyword))
	if kw == "" {
		out := make([]MagpiePlugin, len(all))
		copy(out, all)
		return out
	}
	out := make([]MagpiePlugin, 0, len(all))
	for _, p := range all {
		if strings.Contains(strings.ToLower(p.Name), kw) ||
			strings.Contains(strings.ToLower(p.Repo), kw) ||
			strings.Contains(strings.ToLower(p.Desc), kw) {
			out = append(out, p)
		}
	}
	return out
}
