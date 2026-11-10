// Package cloudflare 是 M5 failover 所需的 Cloudflare API 精简客户端：
// DNS CNAME/TXT upsert（主域名切换、Railway 域名验证）+ Worker KV 读写（节点表）。
//
// token 来自 Option CloudflareApiToken（落库密文，调用方解密后传入），
// 端点可用 CLOUDFLARE_API_ENDPOINT 覆盖（e2e mock；默认官方 v4）。
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// DefaultEndpoint Cloudflare API v4。
const DefaultEndpoint = "https://api.cloudflare.com/client/v4"

// Config Cloudflare 客户端配置。
type Config struct {
	ApiToken      string
	ZoneId        string
	ZoneName      string
	AccountId     string
	KvNamespaceId string
	Endpoint      string
}

// Client Cloudflare API 客户端。
type Client struct {
	Config
	HTTPClient *http.Client
}

// New 创建客户端；Endpoint 缺省读取 CLOUDFLARE_API_ENDPOINT 或官方端点。
func New(cfg Config) *Client {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("CLOUDFLARE_API_ENDPOINT"))
	}
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	cfg.Endpoint = strings.TrimRight(endpoint, "/")
	return &Client{Config: cfg, HTTPClient: &http.Client{Timeout: 30 * time.Second}}
}

// Record DNS 记录。
type Record struct {
	Id      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"`
}

// Zone CF zone。
type Zone struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}

type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cfEnvelope struct {
	Success bool            `json:"success"`
	Errors  []cfError       `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

func (c *Client) do(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(c.ApiToken) == "" {
		return nil, errors.New("cloudflare API token is not configured")
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Endpoint+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.ApiToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var envelope cfEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("cloudflare API returned non-JSON response (http %d): %s", resp.StatusCode, truncate(string(raw), 300))
	}
	if !envelope.Success {
		messages := make([]string, 0, len(envelope.Errors))
		for _, item := range envelope.Errors {
			messages = append(messages, item.Message)
		}
		return nil, fmt.Errorf("cloudflare API error (http %d): %s", resp.StatusCode, strings.Join(messages, "; "))
	}
	return envelope.Result, nil
}

// ResolveZoneId 返回 zone id：优先显式配置，其次按 ZoneName 查询。
func (c *Client) ResolveZoneId(ctx context.Context) (string, error) {
	if strings.TrimSpace(c.ZoneId) != "" {
		return c.ZoneId, nil
	}
	if strings.TrimSpace(c.ZoneName) == "" {
		return "", errors.New("cloudflare zone is not configured (CloudflareZoneId/CloudflareZoneName)")
	}
	raw, err := c.do(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(c.ZoneName), nil)
	if err != nil {
		return "", err
	}
	var zones []Zone
	if err := json.Unmarshal(raw, &zones); err != nil {
		return "", err
	}
	if len(zones) == 0 {
		return "", fmt.Errorf("cloudflare zone %q not found", c.ZoneName)
	}
	return zones[0].Id, nil
}

// ListRecords 列出 zone 下指定类型的 DNS 记录（type 空=全部）。
func (c *Client) ListRecords(ctx context.Context, zoneId, recordType string) ([]Record, error) {
	path := "/zones/" + url.PathEscape(zoneId) + "/dns_records?per_page=100"
	if recordType != "" {
		path += "&type=" + url.QueryEscape(recordType)
	}
	raw, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var records []Record
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, err
	}
	return records, nil
}

// GetRecord 按完整记录名+类型查找单条记录。
func (c *Client) GetRecord(ctx context.Context, zoneId, name, recordType string) (*Record, error) {
	records, err := c.ListRecords(ctx, zoneId, recordType)
	if err != nil {
		return nil, err
	}
	for i := range records {
		if strings.EqualFold(records[i].Name, name) && strings.EqualFold(records[i].Type, recordType) {
			return &records[i], nil
		}
	}
	return nil, nil
}

// UpsertCNAME 创建或更新 CNAME 记录（failover 主域名切换）。
func (c *Client) UpsertCNAME(ctx context.Context, zoneId, name, content string, proxied bool) (*Record, error) {
	return c.upsertRecord(ctx, zoneId, Record{Type: "CNAME", Name: name, Content: content, Proxied: proxied, TTL: 1})
}

// UpsertTXT 创建或更新 TXT 记录（Railway _railway-verify 校验）。
func (c *Client) UpsertTXT(ctx context.Context, zoneId, name, content string) (*Record, error) {
	return c.upsertRecord(ctx, zoneId, Record{Type: "TXT", Name: name, Content: content, TTL: 1})
}

func (c *Client) upsertRecord(ctx context.Context, zoneId string, desired Record) (*Record, error) {
	existing, err := c.GetRecord(ctx, zoneId, desired.Name, desired.Type)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if existing != nil {
		raw, err = c.do(ctx, http.MethodPut, "/zones/"+url.PathEscape(zoneId)+"/dns_records/"+url.PathEscape(existing.Id), desired)
	} else {
		raw, err = c.do(ctx, http.MethodPost, "/zones/"+url.PathEscape(zoneId)+"/dns_records", desired)
	}
	if err != nil {
		return nil, err
	}
	var record Record
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

// GetKV 读取 Worker KV 值；键不存在时返回空串（CF 返回 404）。
func (c *Client) GetKV(ctx context.Context, key string) (string, error) {
	if strings.TrimSpace(c.AccountId) == "" || strings.TrimSpace(c.KvNamespaceId) == "" {
		return "", errors.New("cloudflare KV account/namespace is not configured (CloudflareAccountId/CloudflareKvNamespaceId)")
	}
	if strings.TrimSpace(c.ApiToken) == "" {
		return "", errors.New("cloudflare API token is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	path := "/accounts/" + url.PathEscape(c.AccountId) + "/storage/kv/namespaces/" + url.PathEscape(c.KvNamespaceId) + "/values/" + url.PathEscape(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Endpoint+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.ApiToken)
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("cloudflare KV read failed (http %d): %s", resp.StatusCode, truncate(string(raw), 300))
	}
	// KV values 端点返回原始文本，不是 JSON envelope。
	return string(raw), nil
}

// PutKV 写入 Worker KV 值。
func (c *Client) PutKV(ctx context.Context, key, value string) error {
	if strings.TrimSpace(c.AccountId) == "" || strings.TrimSpace(c.KvNamespaceId) == "" {
		return errors.New("cloudflare KV account/namespace is not configured (CloudflareAccountId/CloudflareKvNamespaceId)")
	}
	path := "/accounts/" + url.PathEscape(c.AccountId) + "/storage/kv/namespaces/" + url.PathEscape(c.KvNamespaceId) + "/values/" + url.PathEscape(key)
	// KV value 是原始文本，不是 JSON 包装；直接发 PUT。
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.Endpoint+path, strings.NewReader(value))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.ApiToken)
	req.Header.Set("Content-Type", "text/plain")
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("cloudflare KV write failed (http %d): %s", resp.StatusCode, truncate(string(raw), 300))
	}
	var envelope cfEnvelope
	if err := json.Unmarshal(raw, &envelope); err == nil && !envelope.Success {
		messages := make([]string, 0, len(envelope.Errors))
		for _, item := range envelope.Errors {
			messages = append(messages, item.Message)
		}
		return fmt.Errorf("cloudflare KV write failed: %s", strings.Join(messages, "; "))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
