package model

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// PublicPoolAdminChannel deliberately excludes credentials, endpoint URLs, provider
// settings and free-form metadata. Those fields can contain reusable secrets.
type PublicPoolAdminChannel struct {
	Id           int    `json:"id"`
	Name         string `json:"name"`
	Type         int    `json:"type"`
	Status       int    `json:"status"`
	Models       string `json:"models"`
	Group        string `json:"group"`
	Priority     *int64 `json:"priority"`
	Weight       *uint  `json:"weight"`
	OwnerId      int    `json:"owner_id"`
	Tier         string `json:"tier"`
	CreatedTime  int64  `json:"created_time"`
	ResponseTime int    `json:"response_time"`
}

var publicPoolAdminColumns = []string{
	"id", "name", "type", "status", "models", "group", "priority", "weight",
	"owner_id", "tier", "created_time", "response_time",
}

// ListPublicPoolAdminChannels includes disabled rows so root can repair them.
// The owner/tier predicate is mandatory and cannot be widened by caller input.
func ListPublicPoolAdminChannels(keyword string, status, start, limit int) ([]PublicPoolAdminChannel, int64, error) {
	items := make([]PublicPoolAdminChannel, 0)
	query := DB.Model(&Channel{}).Where("owner_id = ? AND tier = ?", 0, "free")
	if keyword = strings.TrimSpace(keyword); keyword != "" {
		query = query.Where("name LIKE ? OR models LIKE ?", "%"+keyword+"%", "%"+keyword+"%")
	}
	if status > 0 {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Select(publicPoolAdminColumns).Order("id DESC").Offset(max(0, start)).Limit(min(max(1, limit), 100)).Find(&items).Error
	return items, total, err
}

func GetPublicPoolAdminChannel(id int) (*PublicPoolAdminChannel, error) {
	channel := &PublicPoolAdminChannel{}
	err := DB.Model(&Channel{}).Select(publicPoolAdminColumns).Where("id = ? AND owner_id = ? AND tier = ?", id, 0, "free").First(channel).Error
	return channel, err
}

// PublicPoolComment 公共池模型评论。
// 存的是评论时的昵称/头像快照：用户后来改名或换头像，历史评论的署名不会跟着变，
// 也避免每次列表都多一次 users 表联查。
type PublicPoolComment struct {
	Id          int    `json:"id" gorm:"primaryKey"`
	Model       string `json:"model" gorm:"type:varchar(128);index:idx_ppc_model"`
	ChannelId   int    `json:"channel_id" gorm:"index"`
	ChannelName string `json:"channel_name" gorm:"type:varchar(128)"`
	UserId      int    `json:"user_id" gorm:"index"`
	Username    string `json:"username" gorm:"type:varchar(64)"`
	DisplayName string `json:"display_name" gorm:"type:varchar(64)"`
	Avatar      string `json:"avatar" gorm:"type:varchar(255)"`
	Content     string `json:"content" gorm:"type:text"`
	Rating      int    `json:"rating" gorm:"default:0"`
	CreatedAt   int64  `json:"created_at" gorm:"index"`
}

func (PublicPoolComment) TableName() string { return "public_pool_comments" }

// PublicChannelBrief 公共池里的单个渠道摘要，按模型展开后给前端聚合。
type PublicChannelBrief struct {
	SourceKind   string  `json:"source_kind"`
	ChannelType  int     `json:"-"`
	Enabled      bool    `json:"enabled"`
	ChannelId    int     `json:"channel_id"`
	ChannelName  string  `json:"channel_name"`
	OwnerId      int     `json:"owner_id"`
	OwnerName    string  `json:"owner_name"`
	OwnerAvatar  string  `json:"owner_avatar"`
	Tier         string  `json:"tier"`
	ResponseTime int     `json:"response_time"`
	Model        string  `json:"model"`
	Rating       float64 `json:"rating"`
	Comments     int     `json:"comments"`
}

// PublicChannelModelKey identifies one authorized public route, not a model globally.
type PublicChannelModelKey struct {
	ChannelID int
	ModelName string
}

// PublicRelayLogMetric contains only non-sensitive consume-log aggregates.
// DurationSeconds is end-to-end wall time rounded to seconds by the log writer,
// not generation time. Zero-duration records cannot provide a latency or TPS.
type PublicRelayLogMetric struct {
	ChannelID            int
	ModelName            string
	RequestCount         int64
	TimedCount           int64
	DurationSeconds      float64
	OutputTokens         float64
	TokenDurationSeconds float64
	SampledAt            int64
}

// GetPublicRelayLogMetrics reads platform-free history from the separate LogDB.
// User-shared channels lack a request-time public snapshot, so none of their
// historical logs are publishable. Enabled platform per-pair groups are checked
// again in the main DB. No join across databases or user/content/key reads occur.
func GetPublicRelayLogMetrics(ctx context.Context, briefs []PublicChannelBrief, groups []string) (map[PublicChannelModelKey]PublicRelayLogMetric, error) {
	out := make(map[PublicChannelModelKey]PublicRelayLogMetric)
	if len(briefs) == 0 || len(groups) == 0 || LOG_DB == nil {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	keys := make([]PublicChannelModelKey, 0, len(briefs))
	seen := make(map[PublicChannelModelKey]bool, len(briefs))
	for _, brief := range briefs {
		// Current sharing cannot authorize private-era history. Until requests
		// record a public snapshot, only explicit platform-free sources qualify.
		if brief.OwnerId != 0 || brief.SourceKind != "platform_free" || brief.Tier != "free" {
			continue
		}
		key := PublicChannelModelKey{ChannelID: brief.ChannelId, ModelName: brief.Model}
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	now := time.Now().Unix()
	for start := 0; start < len(keys); start += 100 {
		batch := keys[start:min(start+100, len(keys))]
		abilitiesQuery := DB.WithContext(ctx).Where("enabled = ? AND "+commonGroupCol+" IN ?", true, groups)
		pairs := DB.Where("1 = 0")
		for _, key := range batch {
			pairs = pairs.Or("channel_id = ? AND model = ?", key.ChannelID, key.ModelName)
		}
		var abilities []Ability
		if err := abilitiesQuery.Where(pairs).Find(&abilities).Error; err != nil {
			return nil, errors.New("public relay metrics unavailable")
		}
		allowed := make(map[PublicChannelModelKey][]string)
		for _, ability := range abilities {
			key := PublicChannelModelKey{ChannelID: ability.ChannelId, ModelName: ability.Model}
			allowed[key] = append(allowed[key], ability.Group)
		}
		logPairs := LOG_DB.WithContext(ctx).Where("1 = 0")
		for key, pairGroups := range allowed {
			// Let GORM quote the reserved group column for the LOG database,
			// which may use a different dialect from the primary database.
			logPairs = logPairs.Or(map[string]any{"channel_id": key.ChannelID, "model_name": key.ModelName, "group": pairGroups})
		}
		var rows []PublicRelayLogMetric
		err := LOG_DB.WithContext(ctx).Model(&Log{}).
			Select("channel_id, model_name, COUNT(*) AS request_count, SUM(CASE WHEN use_time > 0 THEN 1 ELSE 0 END) AS timed_count, SUM(CASE WHEN use_time > 0 THEN use_time * 1.0 ELSE 0 END) AS duration_seconds, SUM(CASE WHEN use_time > 0 AND completion_tokens >= 0 THEN completion_tokens * 1.0 ELSE 0 END) AS output_tokens, SUM(CASE WHEN use_time > 0 AND completion_tokens >= 0 THEN use_time * 1.0 ELSE 0 END) AS token_duration_seconds, MAX(created_at) AS sampled_at").
			Where("type = ? AND created_at >= ? AND created_at <= ?", LogTypeConsume, now-86400, now).
			Where(logPairs).Group("channel_id, model_name").Scan(&rows).Error
		if err != nil {
			return nil, errors.New("public relay metrics unavailable")
		}
		for _, row := range rows {
			out[PublicChannelModelKey{ChannelID: row.ChannelID, ModelName: row.ModelName}] = row
		}
	}
	return out, nil
}

// ListPublicChannels lists public entries for internal catalog callers.
func ListPublicChannels() ([]PublicChannelBrief, error) {
	var groups []string
	if err := DB.Model(&Ability{}).Where("enabled = ?", true).Distinct(commonGroupCol).Pluck(commonGroupCol, &groups).Error; err != nil {
		return nil, err
	}
	return ListPublicChannelsForGroups(groups)
}

// ListPublicChannelsForGroups includes only enabled group-authorized public models.
// Owner-zero paid channels are not platform free sources; user channels need opt-in.
func ListPublicChannelsForGroups(groups []string) ([]PublicChannelBrief, error) {
	out := make([]PublicChannelBrief, 0)
	if len(groups) == 0 {
		return out, nil
	}
	var channels []Channel
	ids := DB.Model(&Ability{}).Select("channel_id").Where(commonGroupCol+" IN ? AND enabled = ?", groups, true)
	if err := DB.Where("status = ? AND id IN (?)", common.ChannelStatusEnabled, ids).
		Where("(owner_id = 0 AND tier = ?) OR (owner_id > 0 AND visibility = 1)", "free").Order("response_time ASC, id ASC").Find(&channels).Error; err != nil {
		return nil, err
	}
	var users []User
	ownerIDs := make([]int, 0, len(channels))
	for _, channel := range channels {
		if channel.OwnerId > 0 {
			ownerIDs = append(ownerIDs, channel.OwnerId)
		}
	}
	owners := make(map[int]User)
	if len(ownerIDs) > 0 {
		if err := DB.Select("id, username, display_name, avatar").Where("id IN ?", ownerIDs).Find(&users).Error; err != nil {
			return nil, err
		}
		for _, user := range users {
			owners[user.Id] = user
		}
	}
	var abilities []Ability
	if err := DB.Where(commonGroupCol+" IN ? AND enabled = ?", groups, true).Find(&abilities).Error; err != nil {
		return nil, err
	}
	enabled := make(map[int]map[string]bool)
	for _, ability := range abilities {
		if enabled[ability.ChannelId] == nil {
			enabled[ability.ChannelId] = make(map[string]bool)
		}
		enabled[ability.ChannelId][ability.Model] = true
	}
	ratingMap, countMap, err := publicCommentStats(groups)
	if err != nil {
		return nil, err
	}
	for _, channel := range channels {
		owner := owners[channel.OwnerId]
		ownerName := strings.TrimSpace(owner.DisplayName)
		if ownerName == "" {
			ownerName = owner.Username
		}
		seen := make(map[string]bool)
		for name := range strings.SplitSeq(channel.Models, ",") {
			name = strings.TrimSpace(name)
			if seen[name] || !enabled[channel.Id][name] || !ChannelModelVisibleToOwner(&channel, name, 0) {
				continue
			}
			seen[name] = true
			kind := "user_shared"
			if channel.OwnerId == 0 {
				kind = "platform_free"
			}
			out = append(out, PublicChannelBrief{ChannelId: channel.Id, ChannelName: channel.Name, OwnerId: channel.OwnerId, OwnerName: ownerName, OwnerAvatar: owner.Avatar, Tier: channel.Tier, ResponseTime: channel.ResponseTime, Model: name, Rating: ratingMap[name], Comments: countMap[name], SourceKind: kind, ChannelType: channel.Type, Enabled: true})
		}
	}
	return out, nil
}

// ChannelModelVisibleToOwner is shared by listing and both relay selection paths.
// Platform channels belong to the platform, never to another user's private pool.
// An explicit publicModels subset is fail-closed, including empty/malformed subsets.
func ChannelModelVisibleToOwner(channel *Channel, modelName string, ownerUserID int) bool {
	if channel == nil || channel.Status != common.ChannelStatusEnabled {
		return false
	}
	normalized := ratio_setting.RoutingMatchModelName(modelName)
	servedName := ""
	for name := range strings.SplitSeq(channel.Models, ",") {
		name = strings.TrimSpace(name)
		if name == modelName || name == normalized {
			servedName = name
			break
		}
	}
	if servedName == "" {
		return false
	}
	if channel.OwnerId == 0 || ownerUserID > 0 && channel.OwnerId == ownerUserID {
		return true
	}
	if channel.Visibility != 1 {
		return false
	}
	subset := parsePublicModelsSubset(channel.OtherInfo)
	return subset == nil || subset[servedName]
}

func parsePublicModelsSubset(otherInfoRaw string) map[string]bool {
	trimmed := strings.TrimSpace(otherInfoRaw)
	if trimmed == "" {
		return nil
	}
	var info struct {
		PublicModels *[]string `json:"publicModels"`
	}
	if err := common.Unmarshal([]byte(trimmed), &info); err != nil {
		return map[string]bool{}
	}
	if info.PublicModels == nil {
		if strings.Contains(trimmed, `"publicModels"`) {
			return map[string]bool{}
		}
		return nil
	}
	set := make(map[string]bool, len(*info.PublicModels))
	for _, item := range *info.PublicModels {
		if v := strings.TrimSpace(item); v != "" {
			set[v] = true
		}
	}
	return set
}

// GetModelChannelsForOwner resolves metadata only from channels this viewer may relay.
// The result is request-scoped; private metadata never enters a global public cache.
func GetModelChannelsForOwner(modelNames, groups []string, ownerUserID int) (map[string][]*Channel, error) {
	result := make(map[string][]*Channel)
	if len(modelNames) == 0 || len(groups) == 0 {
		return result, nil
	}
	var rows []struct {
		ModelName string
		Channel
	}
	err := DB.Table("abilities").Select("abilities.model AS model_name, channels.*").
		Joins("JOIN channels ON channels.id = abilities.channel_id").
		Where("abilities.model IN ? AND abilities."+commonGroupCol+" IN ? AND abilities.enabled = ? AND channels.status = ?", modelNames, groups, true, common.ChannelStatusEnabled).
		Where("channels.owner_id = ? OR channels.owner_id = 0 OR channels.visibility = 1", ownerUserID).
		Order("COALESCE(abilities.priority, 0) DESC, abilities.weight DESC, abilities.channel_id ASC").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if ChannelModelVisibleToOwner(&row.Channel, row.ModelName, ownerUserID) {
			channel := row.Channel
			result[row.ModelName] = append(result[row.ModelName], &channel)
		}
	}
	return result, nil
}

// GetModelEndpointOverrides preserves administrator metadata only for authorized names.
// Callers must establish channel eligibility before requesting these records.
func GetModelEndpointOverrides(modelNames []string) (map[string]map[string]common.EndpointInfo, error) {
	result := make(map[string]map[string]common.EndpointInfo)
	if len(modelNames) == 0 {
		return result, nil
	}
	var records []Model
	if err := DB.Find(&records).Error; err != nil {
		return nil, err
	}
	metadata := resolveModelMetadata(records, modelNames)
	for _, name := range modelNames {
		meta := metadata[name]
		if meta == nil || strings.TrimSpace(meta.Endpoints) == "" {
			continue
		}
		var raw map[string]any
		if common.Unmarshal([]byte(meta.Endpoints), &raw) != nil {
			continue
		}
		endpoints := make(map[string]common.EndpointInfo)
		for key, value := range raw {
			info := common.EndpointInfo{Method: "POST"}
			switch value := value.(type) {
			case string:
				info.Path = value
			case map[string]any:
				info.Path, _ = value["path"].(string)
				if method, ok := value["method"].(string); ok {
					info.Method = strings.ToUpper(method)
				}
			default:
				continue
			}
			endpoints[key] = info
		}
		result[name] = endpoints
	}
	return result, nil
}

// publicCommentStats excludes comments on withdrawn/private sources and subsets.
func publicCommentStats(groups []string) (map[string]float64, map[string]int, error) {
	rows, err := visiblePublicComments("", groups)
	if err != nil {
		return nil, nil, err
	}
	ratings := make(map[string]float64)
	counts := make(map[string]int)
	for _, row := range rows {
		ratings[row.Model] += float64(row.Rating)
		counts[row.Model]++
	}
	for name, sum := range ratings {
		ratings[name] = sum / float64(counts[name])
	}
	return ratings, counts, nil
}

// ListPublicComments revalidates current visibility rather than trusting snapshots.
func ListPublicComments(modelName string, limit int, groupFilters ...[]string) ([]PublicPoolComment, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var groups []string
	if len(groupFilters) > 0 {
		groups = groupFilters[0]
	} else if err := DB.Model(&Ability{}).Distinct(commonGroupCol).Pluck(commonGroupCol, &groups).Error; err != nil {
		return nil, err
	}
	rows, err := visiblePublicComments(modelName, groups)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, err
}

func visiblePublicComments(modelName string, groups []string) ([]PublicPoolComment, error) {
	out := make([]PublicPoolComment, 0)
	if len(groups) == 0 {
		return out, nil
	}
	var rows []PublicPoolComment
	q := DB.Model(&PublicPoolComment{}).Select("public_pool_comments.*").
		Joins("JOIN channels ON channels.id = public_pool_comments.channel_id").
		Where("channels.status = ? AND ((channels.owner_id = 0 AND channels.tier = ?) OR (channels.owner_id > 0 AND channels.visibility = 1))", common.ChannelStatusEnabled, "free").Order("public_pool_comments.id DESC")
	if name := strings.TrimSpace(modelName); name != "" {
		q = q.Where("public_pool_comments.model = ?", name)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ChannelId)
	}
	if len(ids) == 0 {
		return out, nil
	}
	var channels []Channel
	if err := DB.Where("id IN ?", ids).Find(&channels).Error; err != nil {
		return nil, err
	}
	byID := make(map[int]*Channel, len(channels))
	for i := range channels {
		byID[channels[i].Id] = &channels[i]
	}
	var abilities []Ability
	if err := DB.Where("channel_id IN ? AND enabled = ? AND "+commonGroupCol+" IN ?", ids, true, groups).Find(&abilities).Error; err != nil {
		return nil, err
	}
	enabled := make(map[int]map[string]bool)
	for _, ability := range abilities {
		if enabled[ability.ChannelId] == nil {
			enabled[ability.ChannelId] = make(map[string]bool)
		}
		enabled[ability.ChannelId][ability.Model] = true
	}
	for _, row := range rows {
		if enabled[row.ChannelId][row.Model] && ChannelModelVisibleToOwner(byID[row.ChannelId], row.Model, 0) {
			out = append(out, row)
		}
	}
	return out, nil
}

// AddPublicComment 落一条评论。
func AddPublicComment(row *PublicPoolComment) error {
	row.CreatedAt = time.Now().Unix()
	return DB.Create(row).Error
}

// SetSelfChannelVisibility 更新自建渠道的可见性与层级。
// 只允许 owner 自己改：where 带上 owner_id，别人的渠道改不动（也改不到平台渠道）。
func SetSelfChannelVisibility(ownerId int, channelId int, visibility int, tier string) error {
	if ownerId <= 0 || channelId <= 0 {
		return errors.New("invalid owner or channel")
	}
	res := DB.Model(&Channel{}).
		Where("id = ? AND owner_id = ?", channelId, ownerId).
		Updates(map[string]any{"visibility": visibility, "tier": tier})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("channel not found or not owned by you")
	}
	return nil
}

// IpAllowedByRules 判断来源 IP 是否命中白名单规则。
// 规则支持逗号/分号/空白/换行分隔，可混用精确 IP 与 CIDR；空规则表示不限制来源。
func IpAllowedByRules(rules string, ip string) bool {
	rules = strings.TrimSpace(rules)
	if rules == "" {
		return true
	}
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return false
	}
	fields := strings.FieldsFunc(rules, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})
	for _, raw := range fields {
		rule := strings.TrimSpace(raw)
		if rule == "" {
			continue
		}
		if strings.Contains(rule, "/") {
			if _, ipnet, err := net.ParseCIDR(rule); err == nil && ipnet.Contains(parsed) {
				return true
			}
			continue
		}
		if target := net.ParseIP(rule); target != nil && target.Equal(parsed) {
			return true
		}
	}
	return false
}

// SetSelfChannelAllowIps 更新自建渠道的免费层订阅 IP 白名单（只能改自己的渠道）。
func SetSelfChannelAllowIps(ownerId int, channelId int, allowIps string) error {
	res := DB.Model(&Channel{}).
		Where("id = ? AND owner_id = ?", channelId, ownerId).
		Update("allow_ips", allowIps)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("channel not found or not owned by you")
	}
	return nil
}

// UpdateUserAvatar 只更新头像字段（公共池署名用）。
func UpdateUserAvatar(uid int, avatar string) error {
	return DB.Model(&User{}).Where("id = ?", uid).Update("avatar", avatar).Error
}
