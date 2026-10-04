package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/custos-machina/backend/internal/modules/ai"
)

var ErrTokenNotFound = errors.New("MCP 凭证不存在")

// ToolsSource 平台数据投影（app 层注入实现，避免 mcp 直连业务表——
// 与 projects.Reader / ai.ChatContextSource 同一投影纪律）。
type ToolsSource interface {
	ListServers(ctx context.Context) []map[string]any
	ListProjects(ctx context.Context) []map[string]any
	ListBuilds(ctx context.Context, projectID uint, limit int) []map[string]any
	ListReleases(ctx context.Context, projectID uint, limit int) []map[string]any
	ListCronRuns(ctx context.Context, limit int) []map[string]any
	ListContainers(ctx context.Context, serverID uint) ([]map[string]any, error)
}

// tokenCtx 请求链路传递已验证凭证（gin 中间件 → StreamableHTTPHandler → tool handler）。
type tokenCtx struct {
	TokenID uint
	Role    string
	Name    string
}

type tokenCtxKey struct{}

func withToken(ctx context.Context, t *tokenCtx) context.Context {
	return context.WithValue(ctx, tokenCtxKey{}, t)
}

func tokenFrom(ctx context.Context) *tokenCtx {
	t, _ := ctx.Value(tokenCtxKey{}).(*tokenCtx)
	return t
}

type Service struct {
	db   *gorm.DB
	src  ToolsSource // app 层注入
	pack ai.ChatContextSource
	mu   sync.Mutex
	srvs map[string]*mcp.Server // 按角色缓存的 server（tools 注册一次）
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db, srvs: map[string]*mcp.Server{}}
}

// SetSources 数据源注入（app 装配期）。
func (s *Service) SetSources(src ToolsSource, pack ai.ChatContextSource) {
	s.src = src
	s.pack = pack
}

// ---- 凭证管理 ----

// IssueTokenOut 签发结果：明文只出现一次。
type IssueTokenOut struct {
	Token
	Plaintext string `json:"plaintext"`
}

func (s *Service) IssueToken(ctx context.Context, name, role string) (*IssueTokenOut, error) {
	if role != TokenRoleAdmin && role != TokenRoleDev {
		return nil, fmt.Errorf("role 取值须为 admin|dev")
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	plaintext := "mcp_" + hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(plaintext))
	t := Token{Name: name, TokenHash: hex.EncodeToString(sum[:]), Role: role, Enabled: true}
	if err := s.db.WithContext(ctx).Create(&t).Error; err != nil {
		return nil, err
	}
	return &IssueTokenOut{Token: t, Plaintext: plaintext}, nil
}

func (s *Service) ListTokens(ctx context.Context) ([]Token, error) {
	var ts []Token
	if err := s.db.WithContext(ctx).Order("id DESC").Find(&ts).Error; err != nil {
		return nil, err
	}
	return ts, nil
}

// Revoke 吊销 = Enabled false（保留记录与审计链；即时生效——Verify 每次查库）。
func (s *Service) Revoke(ctx context.Context, id uint) error {
	res := s.db.WithContext(ctx).Model(&Token{}).Where("id = ?", id).Update("enabled", false)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrTokenNotFound
	}
	return nil
}

func (s *Service) Enable(ctx context.Context, id uint) error {
	res := s.db.WithContext(ctx).Model(&Token{}).Where("id = ?", id).Update("enabled", true)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrTokenNotFound
	}
	return nil
}

// Verify 校验 Bearer 凭证（每次查库：吊销即时生效）。成功顺手更新 LastUsedAt。
func (s *Service) Verify(ctx context.Context, plaintext string) (*tokenCtx, error) {
	sum := sha256.Sum256([]byte(plaintext))
	var t Token
	if err := s.db.WithContext(ctx).Where("token_hash = ?", hex.EncodeToString(sum[:])).
		First(&t).Error; err != nil {
		return nil, errors.New("MCP 凭证无效")
	}
	if !t.Enabled {
		return nil, errors.New("MCP 凭证已吊销")
	}
	now := time.Now()
	s.db.WithContext(ctx).Model(&Token{}).Where("id = ?", t.ID).
		UpdateColumn("last_used_at", &now) // best-effort
	return &tokenCtx{TokenID: t.ID, Role: t.Role, Name: t.Name}, nil
}

// ---- MCP server（按角色构造，tools 一次注册） ----

// ServerFor 返回该角色的 MCP Server（缓存；角色即 allowlist 维度——
// M2 tools 全只读对两角色开放，Sensitive 过滤在 get_context_pack 内；
// M4 写操作工具在此按角色收紧）。
func (s *Service) ServerFor(role string) *mcp.Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	if srv, ok := s.srvs[role]; ok {
		return srv
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "custos-machina", Version: "0.1.0"}, nil)
	s.registerTools(srv, role)
	s.srvs[role] = srv
	return srv
}

func (s *Service) registerTools(srv *mcp.Server, role string) {
	viewer := []string{role}

	// 审计包装：每次 tool 调用留痕（token 信息取自请求 ctx；取不到时
	// 记 0——SDK 未传导 http ctx 的降级路径）
	audit := func(ctx context.Context, tool string, start time.Time, ok bool) {
		t := tokenFrom(ctx)
		tid := uint(0)
		if t != nil {
			tid = t.TokenID
		}
		s.db.WithContext(context.WithoutCancel(ctx)).Create(&Call{
			TokenID: tid, Tool: tool, OK: ok, Ms: int(time.Since(start).Milliseconds()),
		})
	}

	type listArgs struct {
		ProjectID uint `json:"project_id,omitempty" jsonschema:"可选，按项目过滤（0 = 不过滤）"`
		ServerID  uint `json:"server_id,omitempty" jsonschema:"可选，主机 ID"`
		Limit     int  `json:"limit,omitempty" jsonschema:"返回条数上限，默认 10，最大 50"`
	}
	lim := func(n int) int {
		if n <= 0 {
			return 10
		}
		if n > 50 {
			return 50
		}
		return n
	}
	rowsToText := func(rows []map[string]any) string {
		out := ""
		for _, r := range rows {
			out += fmt.Sprintf("%v\n", r)
		}
		if out == "" {
			out = "（无记录）"
		}
		return out
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_servers",
		Description: "列出平台管理的主机（名称/地址/状态）",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		rows := s.src.ListServers(ctx)
		audit(ctx, "list_servers", start, true)
		return textResult(rowsToText(rows)), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_projects",
		Description: "列出平台项目（名称/仓库/git 托管）",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		rows := s.src.ListProjects(ctx)
		audit(ctx, "list_projects", start, true)
		return textResult(rowsToText(rows)), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_builds",
		Description: "近期 CI 构建记录（可按项目过滤）",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a listArgs) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		rows := s.src.ListBuilds(ctx, a.ProjectID, lim(a.Limit))
		audit(ctx, "list_builds", start, true)
		return textResult(rowsToText(rows)), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_releases",
		Description: "近期发布记录（可按项目过滤）",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a listArgs) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		rows := s.src.ListReleases(ctx, a.ProjectID, lim(a.Limit))
		audit(ctx, "list_releases", start, true)
		return textResult(rowsToText(rows)), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_cron_runs",
		Description: "近期定时任务执行记录（含失败原因摘要）",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a listArgs) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		rows := s.src.ListCronRuns(ctx, lim(a.Limit))
		audit(ctx, "list_cron_runs", start, true)
		return textResult(rowsToText(rows)), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_containers",
		Description: "列出指定主机上的容器（名称/镜像/状态）",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a listArgs) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		if a.ServerID == 0 {
			audit(ctx, "list_containers", start, false)
			return nil, nil, errors.New("server_id 必填")
		}
		rows, err := s.src.ListContainers(ctx, a.ServerID)
		audit(ctx, "list_containers", start, err == nil)
		if err != nil {
			return nil, nil, err
		}
		return textResult(rowsToText(rows)), nil, nil
	})

	type packArgs struct {
		ProjectIDs []uint `json:"project_ids,omitempty" jsonschema:"挂载项目 ID 列表"`
		ServerIDs  []uint `json:"server_ids,omitempty" jsonschema:"挂载主机 ID 列表"`
		Hours      int    `json:"hours,omitempty" jsonschema:"时间窗小时数，默认 24"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_context_pack",
		Description: "获取平台运维上下文包（项目概况/近期构建发布/主机事件/cron 失败，" +
			"按凭证角色过滤并经 DLP 脱敏——dev 凭证不含敏感块）",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a packArgs) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		m := ai.Mount{ProjectIDs: a.ProjectIDs, ServerIDs: a.ServerIDs, Hours: a.Hours}
		blocks := s.pack.MountContext(ctx, m, viewer)
		pack := ai.BuildContextPack(viewer, blocks)
		audit(ctx, "get_context_pack", start, true)
		return textResult(pack.Render()), nil, nil
	})
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}
