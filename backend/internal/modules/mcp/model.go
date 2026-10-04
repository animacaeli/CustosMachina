// Package mcp P6-M2 平台 MCP Server 化：平台能力暴露为标准 MCP tools
// （只读先行；写操作 M4 确认层启用）。Claude Desktop / IDE / 任意 MCP 客户端
// 经 Bearer 接入凭证调用；凭证绑定角色，tools 层与 M1 对话共用同一套
// 上下文角色过滤（BuildContextPack）与 DLP 管线——"一套协议两处复用"。
// SDK 选型（2026-10-04 调研定稿）：官方 github.com/modelcontextprotocol/go-sdk
// （Anthropic 维护、v1.8.0、Streamable HTTP 完整支持、跟踪最新协议规范；
// mark3labs/mcp-go 规范跟进滞后，未选）。传输只做 Streamable HTTP。
package mcp

import (
	"time"

	"gorm.io/gorm"
)

// Token 角色（决定 tools 数据视角：Sensitive 块过滤语义同平台用户角色）。
const (
	TokenRoleAdmin = "admin"
	TokenRoleDev   = "dev"
)

// Token MCP 接入凭证（服务间长期凭证，与人凭证分离——M5 方案 B 同一纪律）。
// 明文只在签发时返回一次；库存 SHA-256（不可逆，泄露库不泄露凭证）。
type Token struct {
	ID         uint           `gorm:"primarykey" json:"id"`
	Name       string         `gorm:"size:64;not null" json:"name"`
	TokenHash  string         `gorm:"size:64;uniqueIndex;not null" json:"-"`    // sha256 hex
	Role       string         `gorm:"size:16;not null;default:dev" json:"role"` // admin | dev
	Enabled    bool           `gorm:"not null;default:true" json:"enabled"`
	LastUsedAt *time.Time     `json:"lastUsedAt"`
	CreatedAt  time.Time      `json:"createdAt"`
	UpdatedAt  time.Time      `json:"updatedAt"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Token) TableName() string { return "mcp_tokens" }

// Call 调用留痕（审计：哪个凭证调了哪个工具、耗时与成败）。
type Call struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	TokenID   uint      `gorm:"index" json:"tokenId"`
	Tool      string    `gorm:"size:64;not null" json:"tool"`
	OK        bool      `json:"ok"`
	Ms        int       `json:"ms"`
	CreatedAt time.Time `json:"createdAt"`
}

func (Call) TableName() string { return "mcp_calls" }

// Models 自动迁移模型。
func Models() []any { return []any{&Token{}, &Call{}} }
