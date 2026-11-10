// node.go M5 节点管理数据模型：Node（共享云 DB 多节点部署记录）+ NodeAudit（写操作审计）。
//
// 红线（M5 蓝图 §3.3）：
//   - ApiTokenEnc 只存密文（CRYPTO_SECRET 派生 AES-GCM），API 列表只回脱敏提示；
//   - ProjectId/ServiceId/Domain 进 allowlist，写操作前校验；
//   - 全部写操作落 NodeAudit（动作/目标/结果/操作者）。
package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// Node 平台节点（一个可承载 new-api 的平台服务实例）。
type Node struct {
	Id       int    `json:"id" gorm:"primaryKey;autoIncrement"`
	Name     string `json:"name" gorm:"type:varchar(128);not null"`
	Provider string `json:"provider" gorm:"type:varchar(32);not null;index"` // railway|koyeb|render|fly|sealos|clawcloud|other

	// ApiTokenEnc 平台 API token 的密文（AES-GCM；列表接口绝不返回原文）。
	ApiTokenEnc string `json:"-" gorm:"column:api_token_enc;type:text"`

	// Railway 部署定位（allowlist 来源）。
	WorkspaceId   string `json:"workspace_id" gorm:"type:varchar(64);default:''"`
	ProjectId     string `json:"project_id" gorm:"type:varchar(64);default:''"`
	ServiceId     string `json:"service_id" gorm:"type:varchar(64);default:''"`
	EnvironmentId string `json:"environment_id" gorm:"type:varchar(64);default:''"`
	Repo          string `json:"repo" gorm:"type:varchar(255);default:''"`
	Branch        string `json:"branch" gorm:"type:varchar(128);default:'main'"`
	TargetPort    int    `json:"target_port" gorm:"default:3000"`

	// 访问面：ServiceUrl 为节点健康检查根地址；ServiceDomain 为平台生成的
	// *.up.railway.app；Domain 为用户自定义域名（需 CF DNS/证书验证）。
	ServiceUrl    string `json:"service_url" gorm:"type:varchar(512);default:''"`
	ServiceDomain string `json:"service_domain" gorm:"type:varchar(255);default:''"`
	Domain        string `json:"domain" gorm:"type:varchar(255);default:''"`

	Healthy     bool   `json:"healthy" gorm:"default:false;index"`
	LatencyMs   int    `json:"latency_ms" gorm:"default:0"`
	LastBeat    int64  `json:"last_beat" gorm:"bigint;default:0"`
	BalanceInfo string `json:"balance_info" gorm:"type:text"` // JSON {remaining,currency,updatedAt,source,...}
	LastError   string `json:"last_error" gorm:"type:varchar(512);default:''"`

	Enabled     bool  `json:"enabled" gorm:"default:true;index"`
	CreatedTime int64 `json:"created_time" gorm:"bigint"`
	UpdatedTime int64 `json:"updated_time" gorm:"bigint"`
}

func (Node) TableName() string { return "nodes" }

// NodeAudit 节点管理写操作审计（CheckinLog 同款独立表，便于按 node_id 过滤）。
type NodeAudit struct {
	Id           int    `json:"id" gorm:"primaryKey;autoIncrement"`
	NodeId       int    `json:"node_id" gorm:"not null;index"`
	Action       string `json:"action" gorm:"type:varchar(64);not null;index"` // create|update|delete|check|deploy_plan|deploy_apply|failover_plan|failover_apply|alert
	Target       string `json:"target" gorm:"type:varchar(255);default:''"`
	Result       string `json:"result" gorm:"type:varchar(32);default:''"` // success|failed|rejected
	OperatorId   int    `json:"operator_id" gorm:"default:0;index"`
	OperatorName string `json:"operator_name" gorm:"type:varchar(64);default:''"`
	Detail       string `json:"detail" gorm:"type:text"`
	CreatedTime  int64  `json:"created_time" gorm:"bigint;index"`
}

func (NodeAudit) TableName() string { return "node_audits" }

// ---------------------------------------------------------------------------
// CRUD
// ---------------------------------------------------------------------------

// CreateNode 新建节点；ApiTokenEnc 入参为明文，落库前加密。
func CreateNode(node *Node) error {
	now := common.GetTimestamp()
	node.CreatedTime = now
	node.UpdatedTime = now
	if node.Branch == "" {
		node.Branch = "main"
	}
	if node.TargetPort == 0 {
		node.TargetPort = 3000
	}
	if node.ApiTokenEnc != "" {
		node.ApiTokenEnc = encryptNodeTokenIfNeeded(node.ApiTokenEnc)
	}
	return DB.Create(node).Error
}

// encryptNodeTokenIfNeeded 幂等加密：已加密/plain: 前缀值不重复处理。
func encryptNodeTokenIfNeeded(token string) string {
	if common.IsEncryptedSecret(token) || common.IsPlainPrefixedSecret(token) {
		return token
	}
	return common.EncryptSecretBestEffort(token)
}

// GetNodeById 按 ID 读取节点（含密文 token，调用方需 DecryptedToken）。
func GetNodeById(id int) (*Node, error) {
	var node Node
	if err := DB.First(&node, id).Error; err != nil {
		return nil, err
	}
	return &node, nil
}

// GetNodes 返回全部节点（列表 API 再脱敏）。
func GetNodes() ([]Node, error) {
	var nodes []Node
	err := DB.Order("id desc").Find(&nodes).Error
	return nodes, err
}

// GetEnabledNodes 返回启用节点（巡检/备用节点选择用）。
func GetEnabledNodes() ([]Node, error) {
	var nodes []Node
	err := DB.Where("enabled = ?", true).Order("id").Find(&nodes).Error
	return nodes, err
}

// UpdateNode 更新可编辑字段（token 为空表示保持原值）。
func UpdateNode(node *Node) error {
	node.UpdatedTime = common.GetTimestamp()
	if node.Branch == "" {
		node.Branch = "main"
	}
	if node.ApiTokenEnc != "" {
		node.ApiTokenEnc = encryptNodeTokenIfNeeded(node.ApiTokenEnc)
	}
	updates := map[string]any{
		"name":           node.Name,
		"provider":       node.Provider,
		"workspace_id":   node.WorkspaceId,
		"project_id":     node.ProjectId,
		"service_id":     node.ServiceId,
		"environment_id": node.EnvironmentId,
		"repo":           node.Repo,
		"branch":         node.Branch,
		"target_port":    node.TargetPort,
		"service_url":    node.ServiceUrl,
		"service_domain": node.ServiceDomain,
		"domain":         node.Domain,
		"enabled":        node.Enabled,
		"updated_time":   node.UpdatedTime,
		// 始终写入：node 由 DB 载入时是当前密文；显式 clear_api_token 时为空串。
		"api_token_enc": node.ApiTokenEnc,
	}
	return DB.Model(&Node{}).Where("id = ?", node.Id).Updates(updates).Error
}

// DeleteNode 删除节点。
func DeleteNode(id int) error {
	return DB.Delete(&Node{}, id).Error
}

// UpdateNodeHeartbeat 写入巡检结果。心跳/余额属于运行状态，不更新 updated_time，
// 否则 60s 巡检会让已生成的 deploy plan 频繁过期（plan 以配置版本为一致性命据）。
func UpdateNodeHeartbeat(id int, healthy bool, latencyMs int, errMsg string) error {
	return DB.Model(&Node{}).Where("id = ?", id).Updates(map[string]any{
		"healthy":    healthy,
		"latency_ms": latencyMs,
		"last_beat":  common.GetTimestamp(),
		"last_error": errMsg,
	}).Error
}

// UpdateNodeBalance 写入余额快照 JSON（运行状态，不更新 updated_time）。
func UpdateNodeBalance(id int, balanceJSON string) error {
	return DB.Model(&Node{}).Where("id = ?", id).Updates(map[string]any{
		"balance_info": balanceJSON,
	}).Error
}

// UpdateNodeDeployInfo 部署成功后回写平台资源定位与域名。
func UpdateNodeDeployInfo(id int, projectId, serviceId, environmentId, serviceDomain, domain, serviceUrl string) error {
	updates := map[string]any{"updated_time": common.GetTimestamp()}
	if projectId != "" {
		updates["project_id"] = projectId
	}
	if serviceId != "" {
		updates["service_id"] = serviceId
	}
	if environmentId != "" {
		updates["environment_id"] = environmentId
	}
	if serviceDomain != "" {
		updates["service_domain"] = serviceDomain
	}
	if domain != "" {
		updates["domain"] = domain
	}
	if serviceUrl != "" {
		updates["service_url"] = serviceUrl
	}
	return DB.Model(&Node{}).Where("id = ?", id).Updates(updates).Error
}

// DecryptedToken 返回平台 API token 明文（仅服务端使用，绝不进 API 响应）。
func (node *Node) DecryptedToken() string {
	return common.DecryptSecretOrLegacy(node.ApiTokenEnc)
}

// HasApiToken 是否已配置平台 token。
func (node *Node) HasApiToken() bool {
	return strings.TrimSpace(node.ApiTokenEnc) != ""
}

// ---------------------------------------------------------------------------
// NodeAudit
// ---------------------------------------------------------------------------

// InsertNodeAudit 写一条节点操作审计。
func InsertNodeAudit(audit *NodeAudit) error {
	if audit.CreatedTime == 0 {
		audit.CreatedTime = common.GetTimestamp()
	}
	return DB.Create(audit).Error
}

// GetNodeAudits 按节点（0=全部）倒序分页返回审计记录。
func GetNodeAudits(nodeId, offset, limit int) ([]NodeAudit, int64, error) {
	q := DB.Model(&NodeAudit{})
	if nodeId > 0 {
		q = q.Where("node_id = ?", nodeId)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []NodeAudit
	err := q.Order("id desc").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}
