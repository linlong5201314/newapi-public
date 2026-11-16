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
		// repo 既可以是 GitHub 的 owner/repo，也可以是 npm 包名（以 @ 开头，
		// 如 @magpie-community/opencode-orcaterm-auth）。npm 包的正确安装命令
		// 就是 magpie plugin add <包名>，链接指向 npmjs 页面。
		if strings.HasPrefix(rows[i].Repo, "@") {
			rows[i].Url = "https://www.npmjs.com/package/" + rows[i].Repo
		} else {
			rows[i].Url = "https://github.com/" + rows[i].Repo
		}
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
