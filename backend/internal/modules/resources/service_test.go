package resources

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/pkg/crypto"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"))
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	if err := db.AutoMigrate(&Server{}, &ServerGroup{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	cipher, err := crypto.NewCipher("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("构造加密器失败: %v", err)
	}
	return NewService(NewServerRepository(db), NewGroupRepository(db), db, cipher)
}

func TestCreateServer_CredentialEncrypted(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	out, err := svc.CreateServer(ctx, &CreateServerInput{
		Name: "web-1", Host: "127.0.0.1", Port: 22,
		AuthType: AuthPassword, Username: "root", Password: "s3cret",
	})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	// API 视图不泄漏凭据
	if out.Credential != "" {
		t.Errorf("ServerOut 不应携带凭据密文")
	}
	if !out.HasCredential {
		t.Errorf("HasCredential 应为 true")
	}
	// 落库的是密文，不含明文
	srv, _ := svc.servers.GetByID(ctx, out.ID)
	if srv.Credential == "" || srv.Credential == "s3cret" {
		t.Errorf("凭据应加密落库，实际 %q", srv.Credential)
	}
	if srv.MetricSecs != DefaultMetricSecs {
		t.Errorf("默认采集间隔应为 %d，实际 %d", DefaultMetricSecs, srv.MetricSecs)
	}
}

func TestCreateServer_MissingSecret(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.CreateServer(context.Background(), &CreateServerInput{
		Name: "a", Host: "h", AuthType: AuthPassword, Username: "root",
	})
	if err == nil {
		t.Fatal("密码登录缺少密码应报错")
	}
	_, err = svc.CreateServer(context.Background(), &CreateServerInput{
		Name: "a", Host: "h", AuthType: AuthKey, Username: "root", Password: "x",
	})
	if err == nil {
		t.Fatal("密钥登录缺少私钥应报错")
	}
}

func TestUpdateServer_KeepCredentialWhenOmitted(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	out, _ := svc.CreateServer(ctx, &CreateServerInput{
		Name: "web-1", Host: "127.0.0.1", AuthType: AuthPassword, Username: "root", Password: "s3cret",
	})
	updated, err := svc.UpdateServer(ctx, out.ID, &UpdateServerInput{Name: "web-1-renamed", Port: 2222})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if updated.Name != "web-1-renamed" || updated.Port != 2222 {
		t.Errorf("字段未更新: %+v", updated)
	}
	// 未传凭据时保留旧凭据：能成功解密出原密码
	srv, _ := svc.servers.GetByID(ctx, out.ID)
	cred, err := svc.decryptCredential(srv.Credential)
	if err != nil {
		t.Fatalf("旧凭据应保留: %v", err)
	}
	if cred.Password != "s3cret" || cred.Username != "root" {
		t.Errorf("凭据内容不对: %+v", cred)
	}
}

func TestUpdateServer_ReplaceCredential(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	out, _ := svc.CreateServer(ctx, &CreateServerInput{
		Name: "web-1", Host: "127.0.0.1", AuthType: AuthPassword, Username: "root", Password: "old",
	})
	_, err := svc.UpdateServer(ctx, out.ID, &UpdateServerInput{Password: "new-pass"})
	if err != nil {
		t.Fatalf("仅换密码应沿用用户名: %v", err)
	}
	srv, _ := svc.servers.GetByID(ctx, out.ID)
	cred, _ := svc.decryptCredential(srv.Credential)
	if cred.Password != "new-pass" || cred.Username != "root" {
		t.Errorf("合并结果不对: %+v", cred)
	}
	if srv.Status != StatusUnknown {
		t.Errorf("凭据变更后状态应重置为 unknown，实际 %s", srv.Status)
	}
}

func TestGroupLifecycle(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	g, err := svc.CreateGroup(ctx, &GroupInput{Name: "生产"})
	if err != nil {
		t.Fatalf("建分组失败: %v", err)
	}
	// 空分组可删
	if err := svc.DeleteGroup(ctx, g.ID); err != nil {
		t.Errorf("空分组应可删除: %v", err)
	}
	g2, _ := svc.CreateGroup(ctx, &GroupInput{Name: "预发"})
	if _, err := svc.CreateServer(ctx, &CreateServerInput{
		Name: "s", Host: "h", AuthType: AuthPassword, Username: "root", Password: "p", GroupID: &g2.ID,
	}); err != nil {
		t.Fatalf("分组下建服务器失败: %v", err)
	}
	if err := svc.DeleteGroup(ctx, g2.ID); err == nil {
		t.Error("非空分组删除应报错")
	}
	// 不存在的分组不能挂服务器
	bad := uint(999)
	if _, err := svc.CreateServer(ctx, &CreateServerInput{
		Name: "s2", Host: "h", AuthType: AuthPassword, Username: "root", Password: "p", GroupID: &bad,
	}); err == nil {
		t.Error("挂到不存在的分组应报错")
	}
}

func TestListGroups_Count(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	g, _ := svc.CreateGroup(ctx, &GroupInput{Name: "g1"})
	for _, n := range []string{"a", "b"} {
		if _, err := svc.CreateServer(ctx, &CreateServerInput{
			Name: n, Host: "h", AuthType: AuthPassword, Username: "root", Password: "p", GroupID: &g.ID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := svc.ListGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ServerCount != 2 {
		t.Errorf("分组计数不对: %+v", list)
	}
}

// TestUpdateServerInputBinding 回归：binding tag 拼写错误（如曾经的 omitempty_with）
// 会让 PUT /servers/:id 全部 panic，这里锁定输入结构可正常绑定。
func TestUpdateServerInputBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{"name":"x"}`,
		`{"host":"h","port":22,"metricSecs":30,"username":"root","password":"p","authType":"password"}`,
		`{"remark":""}`,
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("PUT", "/", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		var in UpdateServerInput
		if err := c.ShouldBindJSON(&in); err != nil {
			t.Fatalf("绑定失败 body=%s: %v", body, err)
		}
	}
}
