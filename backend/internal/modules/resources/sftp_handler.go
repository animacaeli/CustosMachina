package resources

import (
	"fmt"
	"net/http"
	"path"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/server"
)

// registerFileRoutes SFTP 文件管理路由（第四阶段 M4）。
// 读：admin/ops/dev；写：admin/ops（casbin 种子控制，handler 不重复拦角色）。
func (h *Handler) registerFileRoutes(r server.Router) {
	g := r.Authed.Group("/server-files")
	{
		g.GET("/:id/list", h.fileList)
		g.GET("/:id/read", h.fileRead)
		g.GET("/:id/download", h.fileDownload)
		g.POST("/:id/write", h.fileWrite)
		g.POST("/:id/upload", h.fileUpload)
		g.POST("/:id/mkdir", h.fileMkdir)
		g.POST("/:id/rename", h.fileRename)
		g.POST("/:id/remove", h.fileRemove)
	}
}

func (h *Handler) fileList(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	dir := c.DefaultQuery("path", "/")
	entries, err := h.svc.SftpList(id, dir)
	if err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"path": dir, "entries": entries})
}

func (h *Handler) fileRead(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	content, err := h.svc.SftpRead(id, c.Query("path"))
	if err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"content": content})
}

func (h *Handler) fileDownload(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	p := c.Query("path")
	name := path.Base(p)
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	n, err := h.svc.SftpDownload(id, p, c.Writer)
	if err != nil {
		// 出错时可能已写部分字节，统一 500 文本
		c.JSON(http.StatusBadGateway, gin.H{"code": 502, "message": err.Error(), "written": n})
		return
	}
}

// fileWrite 在线编辑保存（<=1MB，写前自动备份 .bak）。
func (h *Handler) fileWrite(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		Path    string `json:"path" binding:"required"`
		Content string `json:"content" binding:"required,max=1048576"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.SftpWrite(id, in.Path, []byte(in.Content)); err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	h.svc.RecordEvent(c.Request.Context(), id, "sftp_edit",
		fmt.Sprintf("%s 在线编辑 %s", h.operator(c), in.Path))
	httpx.OK(c, nil)
}

// fileUpload multipart 上传（落 dir 目录，文件名取上传名；>100MB 建议走 scp）。
func (h *Handler) fileUpload(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	dir := c.PostForm("dir")
	fh, err := c.FormFile("file")
	if err != nil || dir == "" {
		httpx.FailBadRequest(c, "需 multipart 字段 file 与 dir")
		return
	}
	f, err := fh.Open()
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	defer f.Close()
	target := path.Join(dir, path.Base(fh.Filename))
	// 流式上传（边读边写，不整读内存）；100MB 上限在 SftpUpload 内统一判
	if err := h.svc.SftpUpload(id, target, f, fh.Size); err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	h.svc.RecordEvent(c.Request.Context(), id, "sftp_upload",
		fmt.Sprintf("%s 上传 %s（%dKB）", h.operator(c), target, fh.Size>>10))
	httpx.OK(c, gin.H{"path": target})
}

func (h *Handler) fileMkdir(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		Path string `json:"path" binding:"required"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.SftpMkdir(id, in.Path); err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}

func (h *Handler) fileRename(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		From string `json:"from" binding:"required"`
		To   string `json:"to" binding:"required"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.SftpRename(id, in.From, in.To); err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}

// fileRemove 删除（目录须为空；dev 只读由 casbin 种子控制：仅 admin/ops 有 POST）。
func (h *Handler) fileRemove(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		Path  string `json:"path" binding:"required"`
		IsDir bool   `json:"isDir"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.SftpRemove(id, in.Path, in.IsDir); err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}
