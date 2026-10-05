// kv.go P6-M7 K/V 配置（键值形态）+ AgileConfig 共存接入。
// 设计定调（用户 2026-10-04）：共存而非收缩——一个模型、两种底层、三种消费
// 形态：文件（SFTP 下发/API 拉取）+ 键值（AgileConfig SDK 热更）。
// 平台为真相源单向推 AgileConfig（OpenAPI 写入）；AgileConfig 控制台手改视为
// 漂移，对账检测比对后告警（不自动覆盖）。AgileConfig 是纯后端 provider：
// 未配置时 K/V 仅平台存储（UI 行为不变——解耦），不阻塞文件视图。
package configs

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	cryptopkg "github.com/custos-machina/backend/internal/pkg/crypto"
)

// ---- 平台 KV 真相源 ----

// ConfigItem K/V 配置项（应用=项目 × 环境 × key）。
type ConfigItem struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	ProjectID uint      `gorm:"index;not null" json:"projectId"`
	Env       string    `gorm:"size:32;not null;default:prod" json:"env"` // prod/canary/test（对齐文件视图 rel_path 首段）
	Key       string    `gorm:"size:128;not null" json:"key"`
	Value     string    `gorm:"type:text" json:"-"` // 敏感项不随列表回明文（reveal 接口，带审计）
	Sensitive bool      `gorm:"not null;default:false" json:"sensitive"`
	Remark    string    `gorm:"size:255" json:"remark"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (ConfigItem) TableName() string { return "config_items" }

// ItemOut 列表输出（敏感值脱敏为占位）。
type ItemOut struct {
	ConfigItem
	Value string `json:"value"`
}

// AgileApp 平台应用 ↔ AgileConfig app 映射（secret AES 落库；appId 约定=项目名）。
type AgileApp struct {
	ID     uint   `gorm:"primarykey" json:"id"`
	App    string `gorm:"size:64;uniqueIndex;not null" json:"app"`
	AppID  string `gorm:"size:64;not null" json:"appId"`
	Secret string `gorm:"size:256" json:"-"` // AES
}

func (AgileApp) TableName() string { return "config_agile_apps" }

// ---- provider 设置（platform_settings，AES 凭据） ----

const (
	settingKVEndpoint = "kv.agile_endpoint" // 如 http://agileconfig:5000
	settingKVUser     = "kv.agile_user"     // admin 用户名（默认 admin）
	settingKVPass     = "kv.agile_pass"     // AES
)

type KVSettingsOut struct {
	Configured bool   `json:"configured"`
	Endpoint   string `json:"endpoint"`
}

func (s *Service) SaveKVSettings(ctx context.Context, endpoint, user, pass string) error {
	set := func(k, v string) error {
		return s.db.WithContext(ctx).Exec(
			`INSERT INTO platform_settings (key, value) VALUES (?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v).Error
	}
	if endpoint != "" {
		if !strings.HasPrefix(endpoint, "http") {
			return fmt.Errorf("endpoint 须为 http(s):// 地址")
		}
		if err := set(settingKVEndpoint, strings.TrimRight(endpoint, "/")); err != nil {
			return err
		}
	}
	if user != "" {
		if err := set(settingKVUser, user); err != nil {
			return err
		}
	}
	if pass != "" {
		if s.cipher == nil {
			return cryptopkg.ErrNoMasterKey
		}
		enc, err := s.cipher.Encrypt(pass)
		if err != nil {
			return err
		}
		if err := set(settingKVPass, enc); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) KVSettings(ctx context.Context) KVSettingsOut {
	var ep, pass string
	s.db.WithContext(ctx).Table("platform_settings").Select("value").
		Where("key = ?", settingKVEndpoint).Scan(&ep)
	s.db.WithContext(ctx).Table("platform_settings").Select("value").
		Where("key = ?", settingKVPass).Scan(&pass)
	return KVSettingsOut{Configured: ep != "" && pass != "", Endpoint: ep}
}

// kvAudit KV 操作审计（server_events，server_id=0 表示平台级事件）。
func (s *Service) kvAudit(ctx context.Context, typ, msg string) {
	s.db.WithContext(context.WithoutCancel(ctx)).Exec(
		`INSERT INTO server_events (server_id, type, message, created_at) VALUES (0, ?, ?, ?)`,
		typ, msg, time.Now())
}

// ---- CRUD ----

type SaveItemInput struct {
	ProjectID uint   `json:"projectId" binding:"required"`
	Env       string `json:"env" binding:"required,oneof=prod canary test"`
	Key       string `json:"key" binding:"required,max=128"`
	Value     string `json:"value" binding:"max=65536"`
	Sensitive bool   `json:"sensitive"`
	Remark    string `json:"remark" binding:"omitempty,max=255"`
}

func (s *Service) ListItems(ctx context.Context, projectID uint, env string) ([]ItemOut, error) {
	var items []ConfigItem
	q := s.db.WithContext(ctx).Where("project_id = ?", projectID)
	if env != "" {
		q = q.Where("env = ?", env)
	}
	if err := q.Order("env, key").Find(&items).Error; err != nil {
		return nil, err
	}
	out := make([]ItemOut, len(items))
	for i, it := range items {
		out[i] = ItemOut{ConfigItem: it}
		if it.Sensitive {
			out[i].Value = "******"
		} else {
			out[i].Value = it.Value
		}
	}
	return out, nil
}

func (s *Service) RevealItem(ctx context.Context, id uint, by string) (string, *ConfigItem, error) {
	var it ConfigItem
	if err := s.db.WithContext(ctx).First(&it, id).Error; err != nil {
		return "", nil, ErrNotFound
	}
	s.kvAudit(ctx, "config_kv_reveal", fmt.Sprintf("K/V %s/%s 明文查看 by %s", it.Env, it.Key, by))
	return it.Value, &it, nil
}

func (s *Service) SaveItem(ctx context.Context, id uint, in SaveItemInput, by string) (*ConfigItem, error) {
	it := ConfigItem{ProjectID: in.ProjectID, Env: in.Env, Key: in.Key,
		Value: in.Value, Sensitive: in.Sensitive, Remark: in.Remark}
	if id == 0 {
		// 同 (project, env, key) 唯一：重复创建明确报错
		var n int64
		s.db.WithContext(ctx).Model(&ConfigItem{}).
			Where("project_id = ? AND env = ? AND key = ?", in.ProjectID, in.Env, in.Key).Count(&n)
		if n > 0 {
			return nil, fmt.Errorf("键 %s 在该应用×环境下已存在", in.Key)
		}
		if err := s.db.WithContext(ctx).Create(&it).Error; err != nil {
			return nil, err
		}
	} else {
		it.ID = id
		// 敏感项编辑留空 = 保持原值（前端不回填明文）
		updates := map[string]any{"env": in.Env, "key": in.Key,
			"sensitive": in.Sensitive, "remark": in.Remark}
		var orig ConfigItem
		if err := s.db.WithContext(ctx).First(&orig, id).Error; err != nil {
			return nil, ErrNotFound
		}
		if !(orig.Sensitive && in.Value == "") {
			updates["value"] = in.Value
		}
		if err := s.db.WithContext(ctx).Model(&ConfigItem{}).Where("id = ?", id).
			Updates(updates).Error; err != nil {
			return nil, err
		}
	}
	s.kvAudit(ctx, "config_kv_save", fmt.Sprintf("K/V %s/%s 保存 by %s（项目#%d）", in.Env, in.Key, by, in.ProjectID))
	return &it, nil
}

func (s *Service) DeleteItem(ctx context.Context, id uint, by string) error {
	var it ConfigItem
	if err := s.db.WithContext(ctx).First(&it, id).Error; err != nil {
		return ErrNotFound
	}
	if err := s.db.WithContext(ctx).Delete(&it).Error; err != nil {
		return err
	}
	s.kvAudit(ctx, "config_kv_delete", fmt.Sprintf("K/V %s/%s 删除 by %s", it.Env, it.Key, by))
	return nil
}

// ---- AgileConfig provider（单向推送 + 对账） ----

// KVItem 推送/对账的键值对（group=env）。
type KVItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// DiffOut 对账结果：平台为真相源，远端与平台的差异均视为漂移（告警不覆盖）。
type DiffOut struct {
	Missing []string `json:"missing"` // 平台有、远端缺（推送遗漏）
	Extra   []string `json:"extra"`   // 远端有、平台无（控制台手加）
	Drifted []string `json:"drifted"` // 两边都有但值不同（控制台手改）
}

// agileClient AgileConfig OpenAPI 客户端（按官方 REST 文档实现）。
// app 配置操作 Basic(appId, secret)；app 管理用 admin 凭证。
type agileClient struct {
	base      string
	adminUser string
	adminPass string
	http      *http.Client
}

func (s *Service) agileClient(ctx context.Context) (*agileClient, error) {
	var ep, user, encPass string
	q := func(key string, dest *string) {
		s.db.WithContext(ctx).Table("platform_settings").Select("value").
			Where("key = ?", key).Scan(dest)
	}
	q(settingKVEndpoint, &ep)
	q(settingKVUser, &user)
	q(settingKVPass, &encPass)
	if ep == "" || encPass == "" {
		return nil, fmt.Errorf("AgileConfig 未配置（管理后台「配置拉取/键值设置」里填写地址与 admin 密码；未配置时 K/V 仅平台存储）")
	}
	if user == "" {
		user = "admin"
	}
	pass, err := s.cipher.Decrypt(encPass)
	if err != nil {
		return nil, fmt.Errorf("AgileConfig 凭据解密失败: %v", err)
	}
	return &agileClient{base: ep, adminUser: user, adminPass: pass,
		http: &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // 自签内网常见
		}}}, nil
}

func (c *agileClient) do(ctx context.Context, method, path, user, pass string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, raw, nil
}

// agileRemoteConfig 远端配置形态（官方 /api/config model 子集）。
type agileRemoteConfig struct {
	ID           string `json:"id"`
	AppID        string `json:"appId"`
	Group        string `json:"group"`
	Key          string `json:"key"`
	Value        string `json:"value"`
	OnlineStatus int    `json:"onlineStatus"` // 0=待上线 1=在线
	Status       int    `json:"status"`       // 0=删除 1=正常
}

// ensureApp 平台应用 → AgileConfig app（appId 约定=应用名）；不存在则创建，
// 映射与 secret 落库（secret AES）。
func (s *Service) ensureAgileApp(ctx context.Context, c *agileClient, app string) (*AgileApp, error) {
	var m AgileApp
	if err := s.db.WithContext(ctx).Where("app = ?", app).First(&m).Error; err == nil {
		return &m, nil
	}
	code, raw, err := c.do(ctx, http.MethodGet, "/api/app", c.adminUser, c.adminPass, nil)
	if err != nil || code != 200 {
		return nil, fmt.Errorf("查询 AgileConfig 应用失败: %v HTTP %d", err, code)
	}
	var apps []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(raw, &apps); err != nil {
		return nil, fmt.Errorf("解析应用列表失败: %v", err)
	}
	var found *struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Secret string `json:"secret"`
	}
	for i := range apps {
		if apps[i].Name == app {
			found = &apps[i]
			break
		}
	}
	if found == nil {
		// 创建（id/secret 由 AgileConfig 生成）
		code, _, err := c.do(ctx, http.MethodPost, "/api/app", c.adminUser, c.adminPass,
			map[string]any{"name": app, "enabled": true})
		if err != nil || code >= 300 {
			return nil, fmt.Errorf("创建 AgileConfig 应用失败: %v HTTP %d", err, code)
		}
		code, raw, err = c.do(ctx, http.MethodGet, "/api/app", c.adminUser, c.adminPass, nil)
		if err != nil || code != 200 {
			return nil, fmt.Errorf("回查应用失败: %v", err)
		}
		if err := json.Unmarshal(raw, &apps); err != nil {
			return nil, err
		}
		for i := range apps {
			if apps[i].Name == app {
				found = &apps[i]
				break
			}
		}
		if found == nil {
			return nil, fmt.Errorf("AgileConfig 应用创建后未找到")
		}
	}
	if found.Secret == "" {
		return nil, fmt.Errorf("AgileConfig 应用 %q 无 secret（请在控制台设置后重试推送）", app)
	}
	encSec, err := s.cipher.Encrypt(found.Secret)
	if err != nil {
		return nil, err
	}
	m = AgileApp{App: app, AppID: found.ID, Secret: encSec}
	if err := s.db.WithContext(ctx).Create(&m).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Service) agileSecret(m *AgileApp) (string, error) {
	return s.cipher.Decrypt(m.Secret)
}

// appConfigs 拉取某应用全部配置（appId/secret 认证）。
func (c *agileClient) appConfigs(ctx context.Context, appID, secret string) ([]agileRemoteConfig, error) {
	code, raw, err := c.do(ctx, http.MethodGet, "/api/config", appID, secret, nil)
	if err != nil || code != 200 {
		return nil, fmt.Errorf("查询配置失败: %v HTTP %d", err, code)
	}
	var cs []agileRemoteConfig
	if err := json.Unmarshal(raw, &cs); err != nil {
		return nil, err
	}
	return cs, nil
}

// PushKV 平台→AgileConfig 单向推送（group=env）：新增/改值（不删除远端——
// 删除属破坏性，交由对账报告）；改动后逐条 publish 上线。
func (s *Service) PushKV(ctx context.Context, projectID uint, env, by string) (int, error) {
	c, err := s.agileClient(ctx)
	if err != nil {
		return 0, err
	}
	var appName string
	if err := s.db.WithContext(ctx).Table("projects").Select("name").
		Where("id = ?", projectID).Scan(&appName).Error; err != nil || appName == "" {
		return 0, fmt.Errorf("项目不存在")
	}
	var items []ConfigItem
	if err := s.db.WithContext(ctx).Where("project_id = ? AND env = ?", projectID, env).
		Order("key").Find(&items).Error; err != nil {
		return 0, err
	}
	if len(items) == 0 {
		return 0, fmt.Errorf("该应用×环境无 K/V 配置可推送")
	}
	m, err := s.ensureAgileApp(ctx, c, appName)
	if err != nil {
		return 0, err
	}
	secret, _ := s.agileSecret(m)
	remote, err := c.appConfigs(ctx, m.AppID, secret)
	if err != nil {
		return 0, err
	}
	remoteByKey := map[string]agileRemoteConfig{}
	for _, r := range remote {
		if r.Group == env && r.Status == 1 {
			remoteByKey[r.Key] = r
		}
	}
	changed := 0
	for _, it := range items {
		if r, ok := remoteByKey[it.Key]; ok {
			if r.Value == it.Value {
				continue // 无变化
			}
			if _, _, err := c.do(ctx, http.MethodPut, "/api/config", m.AppID, secret,
				map[string]any{"id": r.ID, "appId": m.AppID, "group": env,
					"key": it.Key, "value": it.Value}); err != nil {
				return changed, fmt.Errorf("更新 %s 失败: %v", it.Key, err)
			}
		} else {
			if _, _, err := c.do(ctx, http.MethodPost, "/api/config", m.AppID, secret,
				map[string]any{"appId": m.AppID, "group": env,
					"key": it.Key, "value": it.Value}); err != nil {
				return changed, fmt.Errorf("新增 %s 失败: %v", it.Key, err)
			}
		}
		changed++
	}
	// 上线：重拉后对 group=env 的待上线项逐条 publish
	if changed > 0 {
		remote, err = c.appConfigs(ctx, m.AppID, secret)
		if err != nil {
			return changed, fmt.Errorf("回查配置失败（改动已写入，需在控制台手动上线）: %v", err)
		}
		for _, r := range remote {
			if r.Group == env && r.Status == 1 && r.OnlineStatus == 0 {
				if _, _, err := c.do(ctx, http.MethodPost, "/api/config/publish/"+r.ID,
					m.AppID, secret, nil); err != nil {
					return changed, fmt.Errorf("上线 %s 失败: %v", r.Key, err)
				}
			}
		}
	}
	s.kvAudit(ctx, "config_kv_push", fmt.Sprintf("K/V 推送 AgileConfig %s/%s：%d 项（by %s）", appName, env, changed, by))
	return changed, nil
}

// ReconcileKV 对账：远端 vs 平台（group=env）→ 差异即漂移（告警不覆盖）。
func (s *Service) ReconcileKV(ctx context.Context, projectID uint, env, by string) (*DiffOut, error) {
	c, err := s.agileClient(ctx)
	if err != nil {
		return nil, err
	}
	var appName string
	s.db.WithContext(ctx).Table("projects").Select("name").
		Where("id = ?", projectID).Scan(&appName)
	if appName == "" {
		return nil, fmt.Errorf("项目不存在")
	}
	var m AgileApp
	if err := s.db.WithContext(ctx).Where("app = ?", appName).First(&m).Error; err != nil {
		return nil, fmt.Errorf("该应用尚未推送过 AgileConfig（无映射，先推送）")
	}
	secret, _ := s.agileSecret(&m)
	remote, err := c.appConfigs(ctx, m.AppID, secret)
	if err != nil {
		return nil, err
	}
	var items []ConfigItem
	s.db.WithContext(ctx).Where("project_id = ? AND env = ?", projectID, env).Find(&items)

	platformByKey := map[string]string{}
	for _, it := range items {
		platformByKey[it.Key] = it.Value
	}
	diff := &DiffOut{}
	for _, r := range remote {
		if r.Group != env || r.Status != 1 {
			continue
		}
		pv, ok := platformByKey[r.Key]
		if !ok {
			diff.Extra = append(diff.Extra, r.Key)
		} else if pv != r.Value {
			diff.Drifted = append(diff.Drifted, r.Key)
		}
	}
	for k := range platformByKey {
		found := false
		for _, r := range remote {
			if r.Group == env && r.Status == 1 && r.Key == k {
				found = true
				break
			}
		}
		if !found {
			diff.Missing = append(diff.Missing, k)
		}
	}
	if len(diff.Missing)+len(diff.Extra)+len(diff.Drifted) > 0 {
		s.kvAudit(ctx, "config_kv_drift", fmt.Sprintf(
			"AgileConfig 漂移告警 %s/%s：缺 %d / 多 %d / 值异 %d（by %s，检测到漂移——平台为真相源，请以推送覆盖或回改控制台）",
			appName, env, len(diff.Missing), len(diff.Extra), len(diff.Drifted), by))
	}
	return diff, nil
}
