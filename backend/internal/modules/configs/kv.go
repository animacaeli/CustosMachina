// kv.go P6-M7 AgileConfig 共存接入（用户定调修正：纯配置文件一套 UI，
// AgileConfig 是纯后端通道——不引入 K/V 独立存储与独立页面）。
// 模型：配置文件是唯一真相源与唯一编辑入口；env/ini 格式文件天然即键值，
// 「下发」动作在 SFTP 落盘+生效动作之外，同时同步到 AgileConfig（若已配置），
// 应用嵌 AgileConfig SDK 即获得该组键值的热更能力。平台单向推送；
// AgileConfig 控制台手改视为漂移（对账告警不自动覆盖）。
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

	"github.com/custos-machina/backend/internal/modules/identity"
	cryptopkg "github.com/custos-machina/backend/internal/pkg/crypto"
)

// AgileApp 平台应用 ↔ AgileConfig app 映射（secret AES 落库；appId 约定=项目名）。
type AgileApp struct {
	ID     uint   `gorm:"primarykey" json:"id"`
	App    string `gorm:"size:64;uniqueIndex;not null" json:"app"`
	AppID  string `gorm:"size:64;not null" json:"appId"`
	Secret string `gorm:"size:256" json:"-"` // AES
}

// 密文字段绑定（GCM AAD）
const (
	aadKVPass   = "config_kv.agile_pass"
	aadKVSecret = "config_kv.app_secret"
)

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
		return identity.UpsertSetting(s.db, ctx, k, v)
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
		enc, err := s.cipher.Encrypt(pass, aadKVPass)
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
		Where("skey = ?", settingKVEndpoint).Scan(&ep)
	s.db.WithContext(ctx).Table("platform_settings").Select("value").
		Where("skey = ?", settingKVPass).Scan(&pass)
	return KVSettingsOut{Configured: ep != "" && pass != "", Endpoint: ep}
}

// agileConfigured 是否已配置（Deploy 侧判断用，不报错——未配置即跳过同步）。
func (s *Service) agileConfigured(ctx context.Context) bool {
	return s.KVSettings(ctx).Configured
}

// kvAudit 配置中心操作审计（server_events，server_id=0 表示平台级事件）。
func (s *Service) kvAudit(ctx context.Context, typ, msg string) {
	s.db.WithContext(context.WithoutCancel(ctx)).Exec(
		`INSERT INTO server_events (server_id, type, message, created_at) VALUES (0, ?, ?, ?)`,
		typ, msg, time.Now())
}

// KVItem 推送/对账的键值对（AgileConfig group=环境）。
type KVItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// DiffOut 对账结果：文件派生键值为真相源，远端与它的差异均视为漂移（告警不覆盖）。
type DiffOut struct {
	Missing []string `json:"missing"` // 平台有、远端缺（推送遗漏）
	Extra   []string `json:"extra"`   // 远端有、平台无（控制台手加）
	Drifted []string `json:"drifted"` // 两边都有但值不同（控制台手改）
}

// deriveFileKV 文件 → 键值派生：仅 env/ini 格式参与（天然键值，语义清晰；
// yaml/json/toml 属结构化文件，走 SFTP 下发与 M5 拉取形态，不进配置中心）。
func deriveFileKV(files []File) ([]KVItem, error) {
	var out []KVItem
	for _, f := range files {
		var kv map[string]string
		var err error
		if f.Format == FormatINI {
			kv, err = parseINI(f.Content)
		} else if f.Format == "env" {
			kv, err = parseENV(f.Content)
		} else {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("解析 %s 失败: %v", f.RelPath, err)
		}
		for k, v := range kv {
			out = append(out, KVItem{Key: k, Value: v})
		}
	}
	return out, nil
}

// ---- AgileConfig OpenAPI 客户端（按官方 REST 文档） ----

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
			Where("skey = ?", key).Scan(dest)
	}
	q(settingKVEndpoint, &ep)
	q(settingKVUser, &user)
	q(settingKVPass, &encPass)
	if ep == "" || encPass == "" {
		return nil, fmt.Errorf("AgileConfig 未配置（管理后台「配置中心」填写地址与 admin 密码；未配置时仅文件下发/拉取两形态）")
	}
	if user == "" {
		user = "admin"
	}
	pass, err := s.cipher.Decrypt(encPass, aadKVPass)
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

// ensureAgileApp 平台应用 → AgileConfig app（appId 约定=应用名）；
// 不存在则创建，映射与 secret 落库（AES）。
func (s *Service) ensureAgileApp(ctx context.Context, c *agileClient, app string) (*AgileApp, error) {
	var m AgileApp
	if err := s.db.WithContext(ctx).Where("app = ?", app).First(&m).Error; err == nil {
		return &m, nil
	}
	listApps := func() ([]struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Secret string `json:"secret"`
	}, error) {
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
			return nil, err
		}
		return apps, nil
	}
	apps, err := listApps()
	if err != nil {
		return nil, err
	}
	find := func() *struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Secret string `json:"secret"`
	} {
		for i := range apps {
			if apps[i].Name == app {
				return &apps[i]
			}
		}
		return nil
	}
	found := find()
	if found == nil {
		code, _, err := c.do(ctx, http.MethodPost, "/api/app", c.adminUser, c.adminPass,
			map[string]any{"name": app, "enabled": true})
		if err != nil || code >= 300 {
			return nil, fmt.Errorf("创建 AgileConfig 应用失败: %v HTTP %d", err, code)
		}
		if apps, err = listApps(); err != nil {
			return nil, err
		}
		if found = find(); found == nil {
			return nil, fmt.Errorf("AgileConfig 应用创建后未找到")
		}
	}
	if found.Secret == "" {
		return nil, fmt.Errorf("AgileConfig 应用 %q 无 secret（请在控制台设置后重试同步）", app)
	}
	encSec, err := s.cipher.Encrypt(found.Secret, aadKVSecret)
	if err != nil {
		return nil, err
	}
	m = AgileApp{App: app, AppID: found.ID, Secret: encSec}
	if err := s.db.WithContext(ctx).Create(&m).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

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

// envAgileItems 取某项目×环境下参与配置中心的文件（env/ini）并派生键值。
func (s *Service) envAgileItems(ctx context.Context, projectID uint, env string) ([]KVItem, string, error) {
	var appName string
	if err := s.db.WithContext(ctx).Table("projects").Select("name").
		Where("id = ?", projectID).Scan(&appName).Error; err != nil || appName == "" {
		return nil, "", fmt.Errorf("项目不存在")
	}
	var files []File
	if err := s.db.WithContext(ctx).Where("project_id = ?", projectID).Order("rel_path").Find(&files).Error; err != nil {
		return nil, appName, err
	}
	var envFiles []File
	for _, f := range files {
		if firstSeg(f.RelPath) == env && f.Content != "" && (f.Format == "env" || f.Format == FormatINI) {
			envFiles = append(envFiles, f)
		}
	}
	items, err := deriveFileKV(envFiles)
	return items, appName, err
}

// SyncAgile 文件→AgileConfig 单向推送（group=env）：新增/改值（不删除远端——
// 删除属破坏性，交由对账报告）；改动后逐条 publish 上线。
// 入口：下发动作自动触发 + 管理后台手动全量同步。
func (s *Service) SyncAgile(ctx context.Context, projectID uint, env, by string) (int, error) {
	items, appName, err := s.envAgileItems(ctx, projectID, env)
	if err != nil {
		return 0, err
	}
	if len(items) == 0 {
		return 0, fmt.Errorf("该应用×环境无 env/ini 格式配置文件（配置中心仅同步天然键值格式）")
	}
	c, err := s.agileClient(ctx)
	if err != nil {
		return 0, err
	}
	m, err := s.ensureAgileApp(ctx, c, appName)
	if err != nil {
		return 0, err
	}
	secret, _ := s.cipher.Decrypt(m.Secret, aadKVSecret)
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
	// 上线：重拉后对该组待上线项逐条 publish
	if changed > 0 {
		if remote, err = c.appConfigs(ctx, m.AppID, secret); err != nil {
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
	s.kvAudit(ctx, "config_agile_sync", fmt.Sprintf("配置中心同步 %s/%s：%d 项（by %s）", appName, env, changed, by))
	return changed, nil
}

// ReconcileAgile 对账：远端 vs 文件派生键值 → 差异即漂移（告警不覆盖）。
func (s *Service) ReconcileAgile(ctx context.Context, projectID uint, env, by string) (*DiffOut, error) {
	items, appName, err := s.envAgileItems(ctx, projectID, env)
	if err != nil {
		return nil, err
	}
	c, err := s.agileClient(ctx)
	if err != nil {
		return nil, err
	}
	var m AgileApp
	if err := s.db.WithContext(ctx).Where("app = ?", appName).First(&m).Error; err != nil {
		return nil, fmt.Errorf("该应用尚未同步过 AgileConfig（无映射，先下发或手动同步）")
	}
	secret, _ := s.cipher.Decrypt(m.Secret, aadKVSecret)
	remote, err := c.appConfigs(ctx, m.AppID, secret)
	if err != nil {
		return nil, err
	}
	platformByKey := map[string]string{}
	for _, it := range items {
		platformByKey[it.Key] = it.Value
	}
	diff := &DiffOut{}
	remoteKeys := map[string]bool{}
	for _, r := range remote {
		if r.Group != env || r.Status != 1 {
			continue
		}
		remoteKeys[r.Key] = true
		pv, ok := platformByKey[r.Key]
		if !ok {
			diff.Extra = append(diff.Extra, r.Key)
		} else if pv != r.Value {
			diff.Drifted = append(diff.Drifted, r.Key)
		}
	}
	for k := range platformByKey {
		if !remoteKeys[k] {
			diff.Missing = append(diff.Missing, k)
		}
	}
	if len(diff.Missing)+len(diff.Extra)+len(diff.Drifted) > 0 {
		s.kvAudit(ctx, "config_agile_drift", fmt.Sprintf(
			"AgileConfig 漂移告警 %s/%s：缺 %d / 多 %d / 值异 %d（by %s——平台文件为真相源，请以下发覆盖或回改控制台）",
			appName, env, len(diff.Missing), len(diff.Extra), len(diff.Drifted), by))
	}
	return diff, nil
}

// syncAgileAfterDeploy 下发后自动同步配置中心（Deploy 内调用）：
// 已配置 provider 且该文件是 env/ini 才触发；失败不影响下发主体（审计告警）。
func (s *Service) syncAgileAfterDeploy(ctx context.Context, f *File, by string) {
	if !s.agileConfigured(ctx) {
		return
	}
	if f.Format != "env" && f.Format != FormatINI {
		return
	}
	if f.ProjectID == 0 {
		return
	}
	// 脱离请求 ctx：AgileConfig 慢不拖下发响应；失败仅审计
	go func() {
		sctx := context.WithoutCancel(ctx)
		if _, err := s.SyncAgile(sctx, f.ProjectID, firstSeg(f.RelPath), by); err != nil {
			s.kvAudit(sctx, "config_agile_sync_fail", fmt.Sprintf(
				"配置 %q 下发后同步配置中心失败: %v", f.Name, err))
		}
	}()
}
