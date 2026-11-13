package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/modules/identity"
	"github.com/custos-machina/backend/internal/modules/projects"
	"github.com/custos-machina/backend/internal/modules/rbac"
	jwtpkg "github.com/custos-machina/backend/internal/pkg/jwt"
)

func TestProjectInboxRecipientsAndAccess(t *testing.T) {
	db := testDB(t)
	if err := db.AutoMigrate(append(rbac.Models(), &identity.User{}, &projects.Project{}, &projects.Follow{}, &InboxItem{})...); err != nil {
		t.Fatal(err)
	}
	enforcer, stop, err := rbac.NewEnforcer(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	if _, err := rbac.NewService(db, enforcer, identity.NewUserRepository(db)); err != nil {
		t.Fatal(err)
	}
	users := []identity.User{
		{ID: 1, DisplayName: "creator", Roles: "dev"},
		{ID: 2, DisplayName: "administrator", Roles: "admin"},
		{ID: 3, DisplayName: "follower", Roles: "dev"},
		{ID: 4, DisplayName: "disabled", Roles: "dev", Status: identity.StatusDisabled},
		{ID: 5, DisplayName: "other", Roles: "dev"},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	p := projects.Project{Name: "demo", CreatorID: 1}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint{2, 3, 4} {
		if err := db.Create(&projects.Follow{ProjectID: p.ID, UserID: id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc := &Service{db: db}
	event := ProjectEvent{ProjectID: p.ID, Key: "build:1", Title: "Build completed"}
	for range 2 {
		if err := svc.recordInbox(t.Context(), event); err != nil {
			t.Fatal(err)
		}
	}
	var delivered []uint
	if err := db.Model(&InboxItem{}).Order("user_id").Pluck("user_id", &delivered).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(delivered, []uint{1, 2, 3}) {
		t.Fatalf("unexpected recipients or duplicate deliveries: %v", delivered)
	}

	h := NewHandler(svc)
	call := func(method, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set(jwtpkg.CtxClaimsKey, &jwtpkg.Claims{UserID: 3})
		c.Request = httptest.NewRequest(method, "/auth/inbox", strings.NewReader(body))
		h.inbox(c)
		if w.Code != http.StatusOK {
			t.Fatalf("inbox returned %d: %s", w.Code, w.Body)
		}
		return w
	}
	counts := func() (items, unread int) {
		t.Helper()
		var resp struct {
			Data struct {
				Items  []InboxItem
				Unread int
			}
		}
		if err := json.Unmarshal(call(http.MethodGet, "").Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return len(resp.Data.Items), resp.Data.Unread
	}
	if n, unread := counts(); n != 1 || unread != 1 {
		t.Fatalf("expected one unread message, got %d/%d", n, unread)
	}
	call(http.MethodPut, `{"action":"read-all"}`)
	if n, unread := counts(); n != 1 || unread != 0 {
		t.Fatalf("read state did not persist: %d/%d", n, unread)
	}
	if err := db.Model(&users[2]).Update("roles", "guest").Error; err != nil {
		t.Fatal(err)
	}
	if n, unread := counts(); n != 0 || unread != 0 {
		t.Fatalf("inbox exposed a project after permission removal: %d/%d", n, unread)
	}
	if err := db.Delete(&p).Error; err != nil {
		t.Fatal(err)
	}
	event = ProjectEvent{ProjectID: p.ID, PlatformOnMissingProject: true, Key: "backup:1", Title: "Backup completed"}
	if err := svc.recordInbox(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	var items []InboxItem
	if err := db.Where("event_key = ?", event.Key).Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].UserID != users[1].ID || items[0].ProjectID != 0 {
		t.Fatalf("expected one platform notification for the administrator: %+v", items)
	}
}
