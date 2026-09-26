package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/pkg/crypto"
)

// ServerOut 是对外的服务器视图：永不包含凭据。
type ServerOut struct {
	Server
	HasCredential bool `json:"hasCredential"`
}

func toOut(s *Server) ServerOut {
	out := ServerOut{Server: *s, HasCredential: s.Credential != ""}
	out.Credential = "" // 结构体拷贝会带上密文，视图层显式清空
	return out
}

// GroupWithCount 分组视图（带服务器数）。
type GroupWithCount struct {
	ServerGroup
	ServerCount int64 `json:"serverCount"`
}

type Service struct {
	servers  *ServerRepository
	groups   *GroupRepository
	eventsDB *gorm.DB // 事件表查询（M2：server_events）
	cipher   *crypto.Cipher
}

func NewService(servers *ServerRepository, groups *GroupRepository, db *gorm.DB, cipher *crypto.Cipher) *Service {
	s := &Service{servers: servers, groups: groups, eventsDB: db, cipher: cipher}
	hostKeys = s // 模块内单例（wire 只构造一个 Service），供包级 SSH 函数做 TOFU 校验
	return s
}

// pinned / pin 实现 hostKeyStore（主机公钥持久化在 servers.host_key）。
func (s *Service) pinned(serverID uint) (string, error) {
	var row struct{ HostKey string }
	if err := s.eventsDB.Table("servers").Select("host_key").
		Where("id = ?", serverID).First(&row).Error; err != nil {
		return "", err
	}
	return row.HostKey, nil
}

func (s *Service) pin(serverID uint, keyB64 string) error {
	return s.eventsDB.Model(&Server{}).Where("id = ?", serverID).
		Update("host_key", keyB64).Error
}

// ---- 服务器 ----

type CreateServerInput struct {
	Name       string `json:"name" binding:"required,max=64"`
	Host       string `json:"host" binding:"required,max=255"`
	Port       int    `json:"port" binding:"min=1,max=65535"`
	GroupID    *uint  `json:"groupId" binding:"omitempty"`
	AuthType   string `json:"authType" binding:"required,oneof=password key"`
	Username   string `json:"username" binding:"required,max=64"`
	Password   string `json:"password" binding:"omitempty,max=512"`
	PrivateKey string `json:"privateKey" binding:"omitempty"`
	Passphrase string `json:"passphrase" binding:"omitempty,max=128"`
	MetricSecs int    `json:"metricSecs" binding:"omitempty,oneof=15 30 60"`
	Remark     string `json:"remark" binding:"max=255"`
}

type UpdateServerInput struct {
	Name       string  `json:"name" binding:"omitempty,max=64"`
	Host       string  `json:"host" binding:"omitempty,max=255"`
	Port       int     `json:"port" binding:"omitempty,min=1,max=65535"`
	GroupID    *uint   `json:"groupId"`
	MetricSecs int     `json:"metricSecs" binding:"omitempty,oneof=15 30 60"`
	Remark     *string `json:"remark"`
	// 凭据可选更新：全空则保留原凭据（合并语义在 UpdateServer 里手工处理，不靠 tag）
	Username   string `json:"username" binding:"omitempty,max=64"`
	AuthType   string `json:"authType" binding:"omitempty,oneof=password key"`
	Password   string `json:"password" binding:"omitempty,max=512"`
	PrivateKey string `json:"privateKey" binding:"omitempty"`
	Passphrase string `json:"passphrase" binding:"omitempty,max=128"`
}

func (s *Service) encryptCredential(authType string, c credential) (string, error) {
	if s.cipher == nil {
		return "", errors.New("未配置主密钥 CUSTOS_SECRETS_MASTER_KEY，无法保存凭据")
	}
	if c.Password == "" && c.PrivateKey == "" {
		return "", errors.New("密码与私钥至少提供一项")
	}
	if authType == AuthPassword && c.Password == "" {
		return "", errors.New("密码登录方式缺少密码")
	}
	if authType == AuthKey && c.PrivateKey == "" {
		return "", errors.New("密钥登录方式缺少私钥")
	}
	blob, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return s.cipher.Encrypt(string(blob))
}

func (s *Service) decryptCredential(enc string) (*credential, error) {
	if s.cipher == nil {
		return nil, errors.New("未配置主密钥 CUSTOS_SECRETS_MASTER_KEY，凭据不可用")
	}
	plain, err := s.cipher.Decrypt(enc)
	if err != nil {
		return nil, fmt.Errorf("凭据解密失败: %w", err)
	}
	var c credential
	if err := json.Unmarshal([]byte(plain), &c); err != nil {
		return nil, fmt.Errorf("凭据数据损坏: %w", err)
	}
	return &c, nil
}

func (s *Service) checkGroupExists(ctx context.Context, id *uint) error {
	if id == nil {
		return nil
	}
	if _, err := s.groups.GetByID(ctx, *id); err != nil {
		return fmt.Errorf("分组不存在")
	}
	return nil
}

func (s *Service) ListServers(ctx context.Context) ([]ServerOut, error) {
	list, err := s.servers.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ServerOut, len(list))
	for i := range list {
		out[i] = toOut(&list[i])
	}
	return out, nil
}

func (s *Service) CreateServer(ctx context.Context, in *CreateServerInput) (*ServerOut, error) {
	if err := s.checkGroupExists(ctx, in.GroupID); err != nil {
		return nil, err
	}
	if in.MetricSecs == 0 {
		in.MetricSecs = DefaultMetricSecs
	}
	enc, err := s.encryptCredential(in.AuthType, credential{
		Username:   in.Username,
		Password:   in.Password,
		PrivateKey: in.PrivateKey,
		Passphrase: in.Passphrase,
	})
	if err != nil {
		return nil, err
	}
	srv := &Server{
		Name: in.Name, Host: in.Host, Port: in.Port, GroupID: in.GroupID,
		AuthType: in.AuthType, Credential: enc, MetricSecs: in.MetricSecs, Remark: in.Remark,
	}
	if err := s.servers.Create(ctx, srv); err != nil {
		return nil, err
	}
	out := toOut(srv)
	return &out, nil
}

func (s *Service) UpdateServer(ctx context.Context, id uint, in *UpdateServerInput) (*ServerOut, error) {
	srv, err := s.servers.GetByID(ctx, id)
	if err != nil {
		return nil, gorm.ErrRecordNotFound
	}
	if in.Name != "" {
		srv.Name = in.Name
	}
	if in.Host != "" {
		srv.Host = in.Host
	}
	if in.Port != 0 {
		srv.Port = in.Port
	}
	if in.GroupID != nil {
		if err := s.checkGroupExists(ctx, in.GroupID); err != nil {
			return nil, err
		}
		srv.GroupID = in.GroupID
	}
	if in.MetricSecs != 0 {
		srv.MetricSecs = in.MetricSecs
	}
	if in.Remark != nil {
		srv.Remark = *in.Remark
	}
	// 凭据可选更新：全部留空则保留原凭据；任一提供则按"未填项沿用旧值"合并后整体重加密
	if in.Username != "" || in.Password != "" || in.PrivateKey != "" {
		old, err := s.decryptCredential(srv.Credential)
		if err != nil {
			return nil, err
		}
		if in.Username == "" {
			in.Username = old.Username
		}
		if in.Password == "" && in.PrivateKey == "" {
			if srv.AuthType == AuthPassword {
				in.Password = old.Password
			} else {
				in.PrivateKey = old.PrivateKey
				if in.Passphrase == "" {
					in.Passphrase = old.Passphrase
				}
			}
		}
		authType := in.AuthType
		if authType == "" {
			authType = srv.AuthType
		}
		enc, err := s.encryptCredential(authType, credential{
			Username: in.Username, Password: in.Password,
			PrivateKey: in.PrivateKey, Passphrase: in.Passphrase,
		})
		if err != nil {
			return nil, err
		}
		srv.AuthType = authType
		srv.Credential = enc
		srv.Status = StatusUnknown // 凭据变更即失效旧连接（M2 连接池消费此状态）
	}
	if err := s.servers.Update(ctx, srv); err != nil {
		return nil, err
	}
	out := toOut(srv)
	return &out, nil
}

func (s *Service) DeleteServer(ctx context.Context, id uint) error {
	return s.servers.Delete(ctx, id)
}

// TestServer 连通性测试：成功回写 reachable + lastSeen，失败回写 unreachable 并返回错误。
func (s *Service) TestServer(ctx context.Context, id uint) (bool, string) {
	srv, err := s.servers.GetByID(ctx, id)
	if err != nil {
		return false, "服务器不存在"
	}
	cred, err := s.decryptCredential(srv.Credential)
	if err != nil {
		return false, err.Error()
	}
	err = TestConnectivity(srv.Host, srv.Port, cred)
	now := time.Now()
	if err != nil {
		srv.Status = StatusUnreachable
	} else {
		srv.Status = StatusReachable
		srv.LastSeen = &now
	}
	_ = s.servers.Update(ctx, srv)
	if err != nil {
		return false, err.Error()
	}
	return true, "连接成功"
}

// EventsFor 最近 50 条服务器事件（时间倒序）。
func (s *Service) EventsFor(ctx context.Context, serverID uint) ([]ServerEvent, error) {
	var out []ServerEvent
	err := s.eventsDB.WithContext(ctx).
		Where("server_id = ?", serverID).Order("id desc").Limit(50).
		Find(&out).Error
	return out, err
}

// recordTerminalEvent 终端会话关闭时落事件（M3 审计线索，timeline 桥接点）。
func (s *Service) recordTerminalEvent(ctx context.Context, srv *Server, operator string, d time.Duration) {
	ev := ServerEvent{
		ServerID: srv.ID, Type: "terminal_session",
		Message: fmt.Sprintf("%s 登录终端 %s:%d，时长 %s", operator, srv.Host, srv.Port,
			d.Round(time.Second)),
	}
	_ = s.eventsDB.WithContext(ctx).Create(&ev).Error
}

// serverWithCredential 取服务器并解密凭据（容器/探测/部署入口共用）。
func (s *Service) serverWithCredential(ctx context.Context, id uint) (*Server, *credential, error) {
	srv, err := s.servers.GetByID(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("服务器不存在")
	}
	cred, err := s.decryptCredential(srv.Credential)
	if err != nil {
		return nil, nil, err
	}
	return srv, cred, nil
}

// recordSimpleEvent 通用事件落库。
func (s *Service) recordSimpleEvent(ctx context.Context, serverID uint, typ, msg string) {
	_ = s.eventsDB.WithContext(ctx).Create(&ServerEvent{
		ServerID: serverID, Type: typ, Message: truncate(msg, 255),
	}).Error
}

// ---- 分组 ----

type GroupInput struct {
	Name   string `json:"name" binding:"required,max=64"`
	Remark string `json:"remark" binding:"max=255"`
}

func (s *Service) ListGroups(ctx context.Context) ([]GroupWithCount, error) {
	groups, err := s.groups.List(ctx)
	if err != nil {
		return nil, err
	}
	// 一次拉全量服务器在内存里数（服务器规模 < 千级，避免按分组 N 次 count 查询）
	all, err := s.servers.List(ctx)
	if err != nil {
		return nil, err
	}
	counts := make(map[uint]int64, len(groups))
	for i := range all {
		if all[i].GroupID != nil {
			counts[*all[i].GroupID]++
		}
	}
	out := make([]GroupWithCount, len(groups))
	for i := range groups {
		out[i] = GroupWithCount{ServerGroup: groups[i], ServerCount: counts[groups[i].ID]}
	}
	return out, nil
}

func (s *Service) CreateGroup(ctx context.Context, in *GroupInput) (*ServerGroup, error) {
	g := &ServerGroup{Name: in.Name, Remark: in.Remark}
	if err := s.groups.Create(ctx, g); err != nil {
		return nil, err
	}
	return g, nil
}

func (s *Service) UpdateGroup(ctx context.Context, id uint, in *GroupInput) (*ServerGroup, error) {
	g, err := s.groups.GetByID(ctx, id)
	if err != nil {
		return nil, gorm.ErrRecordNotFound
	}
	g.Name = in.Name
	g.Remark = in.Remark
	if err := s.groups.Update(ctx, g); err != nil {
		return nil, err
	}
	return g, nil
}

func (s *Service) DeleteGroup(ctx context.Context, id uint) error {
	n, err := s.groups.CountByGroup(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("分组下仍有 %d 台服务器，请先移出", n)
	}
	return s.groups.Delete(ctx, id)
}
