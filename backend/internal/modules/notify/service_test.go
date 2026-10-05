package notify

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	crypto "github.com/custos-machina/backend/internal/pkg/crypto"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	if err := db.AutoMigrate(&Group{}, &SendRecord{}, &identitySettingTable{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return db
}

// identitySettingTable 仅用于建表（platform_settings 属 identity 模块，这里只建同名表避免跨模块依赖）。
type identitySettingTable struct {
	Key   string `gorm:"primarykey;size:64"`
	Value string `gorm:"type:text"`
}

func (identitySettingTable) TableName() string { return "platform_settings" }

func TestValidScope(t *testing.T) {
	for scope, ok := range map[string]bool{ScopeProd: true, ScopeDev: true, "other": false, "": false} {
		if got := validScope(scope); got != ok {
			t.Errorf("validScope(%q) = %v, want %v", scope, got, ok)
		}
	}
}

func TestDetectProvider(t *testing.T) {
	cases := map[string]provider{
		"https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=x": provWecom,
		"https://oapi.dingtalk.com/robot/send?access_token=x":    provDingtalk,
		"https://open.feishu.cn/open-apis/bot/v2/hook/x":         provFeishu,
		"https://example.com/hook":                               provUnknown,
	}
	for url, want := range cases {
		if got := detectProvider(url); got != want {
			t.Errorf("detectProvider(%q) = %v, want %v", url, got, want)
		}
	}
}

func TestGroupCRUD_WebhookMasked(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil)

	if _, err := svc.Create(t.Context(), SaveGroupInput{Name: "值班群", Scope: "bad"}); err == nil {
		t.Fatal("非法 scope 应被拒绝")
	}

	g, err := svc.Create(t.Context(), SaveGroupInput{Name: "值班群", Scope: ScopeProd})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if g.Scope != ScopeProd || g.HasWebhook {
		t.Fatalf("scope/webhook 状态异常: %+v", g)
	}

	// 更新留空 webhook = 保留
	if _, err := svc.Update(t.Context(), g.ID, SaveGroupInput{Name: "值班群", Scope: ScopeProd, Remark: "r"}); err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	list, err := svc.List(t.Context())
	if err != nil || len(list) != 1 {
		t.Fatalf("列表异常: %v %d", err, len(list))
	}
	if list[0].Webhook != "" {
		t.Fatal("列表不得回传 webhook（即便密文）")
	}
}

func TestSend_NoWebhook(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil)
	g, err := svc.Create(t.Context(), SaveGroupInput{Name: "测试群", Scope: ScopeDev})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	full, err := svc.Get(t.Context(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Send(t.Context(), full, "标题", "内容"); err == nil {
		t.Fatal("未配置 webhook 的群发送应报错")
	}
	// 留痕应有 failed 记录
	var cnt int64
	db.Model(&SendRecord{}).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("发送留痕数 = %d, want 1", cnt)
	}
}

// binding tag 回归：未知 tag 会 panic（第二阶段审核教训），新 input struct 必须过绑定测试。
func TestSaveGroupInputBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{`{"name":"值班群","scope":"prod"}`, `{"name":"x","scope":"dev","webhook":"https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=k","remark":"r"}`} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("PUT", "/", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		var in SaveGroupInput
		if err := c.ShouldBindJSON(&in); err != nil {
			t.Fatalf("绑定失败 body=%s: %v", body, err)
		}
	}
}

// ---- P6-M9 渠道泛化：Telegram / SMTP ----

// fakeTelegram 本地模拟 Bot API（telegramBase 注入）。
func fakeTelegram(t *testing.T) (*httptest.Server, *map[string]string) {
	t.Helper()
	got := map[string]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("/bottok/sendMessage", func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		got["chat_id"], _ = m["chat_id"].(string)
		got["text"], _ = m["text"].(string)
		got["parse_mode"], _ = m["parse_mode"].(string)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &got
}

func notifyTestService(t *testing.T) *Service {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	if err := db.AutoMigrate(&Group{}, &SendRecord{}, &Rule{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if err := db.Exec(`CREATE TABLE platform_settings (key TEXT PRIMARY KEY, value TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	cipher, err := crypto.NewCipher("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	return NewService(db, cipher)
}

func TestSendTelegramChannel(t *testing.T) {
	srv, got := fakeTelegram(t)
	old := telegramBase
	telegramBase = srv.URL
	t.Cleanup(func() { telegramBase = old })

	svc := notifyTestService(t)
	ctx := context.Background()
	if err := svc.SaveChannelSettings(ctx, "tok", "", "", "", "", ""); err != nil {
		t.Fatalf("保存渠道设置失败: %v", err)
	}
	out, err := svc.Create(ctx, SaveGroupInput{
		Name: "【P】tg 告警群", Scope: ScopeProd, Channel: ChannelTelegram, Target: "-100123",
	})
	if err != nil {
		t.Fatalf("建群失败: %v", err)
	}
	g, _ := svc.Get(ctx, out.ID)
	if err := svc.Send(ctx, g, "磁盘告警", "使用率 **91%**"); err != nil {
		t.Fatalf("发送失败: %v", err)
	}
	if (*got)["chat_id"] != "-100123" || (*got)["parse_mode"] != "Markdown" {
		t.Fatalf("Telegram 载荷异常: %v", *got)
	}
	if !strings.Contains((*got)["text"], "*磁盘告警*") || !strings.Contains((*got)["text"], "91%") {
		t.Fatalf("Telegram 文本异常: %q", (*got)["text"])
	}
	// 留痕
	var rec SendRecord
	svc.db.Last(&rec)
	if rec.Status != "ok" {
		t.Fatalf("留痕异常: %+v", rec)
	}
}

// fakeSMTP 最小 SMTP 应答机（记录 DATA 正文）。
func fakeSMTP(t *testing.T) (string, *[]string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mails := &[]string{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				write := func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }
				write("220 fake smtp")
				inData := false
				var body []string
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimRight(line, "\r\n")
					if inData {
						if line == "." {
							inData = false
							*mails = append(*mails, strings.Join(body, "\n"))
							write("250 ok")
							continue
						}
						body = append(body, line)
						continue
					}
					switch {
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
						write("250-fake")
						write("250 AUTH PLAIN")
					case strings.HasPrefix(line, "AUTH"):
						write("235 ok")
					case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
						write("250 ok")
					case line == "DATA":
						inData = true
						write("354 go")
					case line == "QUIT":
						write("221 bye")
						return
					default:
						write("250 ok")
					}
				}
			}(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String(), mails
}

func TestSendSMTPChannel(t *testing.T) {
	addr, mails := fakeSMTP(t)
	host, port, _ := strings.Cut(addr, ":")
	svc := notifyTestService(t)
	ctx := context.Background()
	if err := svc.SaveChannelSettings(ctx, "", host, port, "alert@x.com", "pass", "noreply@x.com"); err != nil {
		t.Fatalf("保存渠道设置失败: %v", err)
	}
	out, err := svc.Create(ctx, SaveGroupInput{
		Name: "【P】邮件值班", Scope: ScopeProd, Channel: ChannelSMTP, Target: "a@x.com, b@x.com",
	})
	if err != nil {
		t.Fatalf("建群失败: %v", err)
	}
	g, _ := svc.Get(ctx, out.ID)
	if err := svc.Send(ctx, g, "证书到期", "证书 **api.example.com** 将在 3 天后到期"); err != nil {
		t.Fatalf("发送失败: %v", err)
	}
	if len(*mails) != 1 {
		t.Fatalf("发信数 = %d", len(*mails))
	}
	m := (*mails)[0]
	if !strings.Contains(m, "To: a@x.com, b@x.com") || !strings.Contains(m, "From: noreply@x.com") {
		t.Fatalf("邮件头异常: %s", m)
	}
	if !strings.Contains(m, "证书 api.example.com") {
		t.Fatalf("markdown 降级异常（** 未剥离）: %s", m)
	}
}

func TestGroupTargetValidation(t *testing.T) {
	svc := notifyTestService(t)
	if _, err := svc.Create(context.Background(), SaveGroupInput{
		Name: "x", Scope: ScopeDev, Channel: ChannelTelegram}); err == nil {
		t.Fatal("telegram 缺 chat id 应拒")
	}
	if _, err := svc.Create(context.Background(), SaveGroupInput{
		Name: "y", Scope: ScopeDev, Channel: ChannelSMTP, Target: "not-an-email"}); err == nil {
		t.Fatal("smtp 非邮箱应拒")
	}
}
