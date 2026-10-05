// terminal_audit.go P6-M6 终端会话审计：录制文件（asciinema v2 cast；历史
// .log 为无时序原始流）扫盘聚合列表 + 内容读取（旧格式即时转单帧 cast）。
// 无独立会话表——文件即真相（保留期 7 天由清理任务滚动删除）。
package resources

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// TerminalSessionOut 会话审计记录。
type TerminalSessionOut struct {
	ServerID uint      `json:"serverId"`
	Server   string    `json:"server"`
	File     string    `json:"file"`
	Operator string    `json:"operator"`
	Start    time.Time `json:"start"`
	Size     int64     `json:"size"`
	Legacy   bool      `json:"legacy"` // 旧 .log：无时序，回放为单帧整流
}

func parseCastMeta(firstLine []byte, out *TerminalSessionOut) {
	var head struct {
		Title     string `json:"title"`
		Timestamp int64  `json:"timestamp"`
		Env       struct {
			Operator string `json:"operator"`
			Server   string `json:"server"`
			ServerID string `json:"serverId"`
		} `json:"env"`
	}
	if json.Unmarshal(firstLine, &head) != nil {
		return
	}
	if head.Env.Operator != "" {
		out.Operator = head.Env.Operator
	} else if i := strings.Index(head.Title, "@"); i > 0 {
		out.Operator = head.Title[:i]
	}
	if head.Env.Server != "" {
		out.Server = head.Env.Server
	}
	if head.Timestamp > 0 {
		out.Start = time.Unix(head.Timestamp, 0)
	}
}

// parseLegacyMeta 旧 .log 首行元数据：{"server":..,"serverId":..,"operator":..,"start":RFC3339}。
func parseLegacyMeta(firstLine []byte, out *TerminalSessionOut) {
	var meta struct {
		Server   string `json:"server"`
		ServerID uint   `json:"serverId"`
		Operator string `json:"operator"`
		Start    string `json:"start"`
	}
	if json.Unmarshal(firstLine, &meta) != nil {
		return
	}
	out.Server, out.Operator = meta.Server, meta.Operator
	if t, err := time.Parse(time.RFC3339, meta.Start); err == nil {
		out.Start = t
	}
}

// ListTerminalSessions 会话列表（start 倒序）；serverID=0 扫全部主机。
func (s *Service) ListTerminalSessions(serverID uint) ([]TerminalSessionOut, error) {
	var dirs []string
	if serverID > 0 {
		dirs = []string{filepath.Join(terminalAuditDir, fmt.Sprintf("%d", serverID))}
	} else {
		entries, err := os.ReadDir(terminalAuditDir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, filepath.Join(terminalAuditDir, e.Name()))
			}
		}
	}
	var out []TerminalSessionOut
	for _, dir := range dirs {
		var sid uint
		fmt.Sscanf(filepath.Base(dir), "%d", &sid)
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, fe := range files {
			name := fe.Name()
			if filepath.Base(name) != name || name == "" {
				continue
			}
			rec := TerminalSessionOut{ServerID: sid, File: name, Operator: "unknown"}
			info, err := fe.Info()
			if err != nil {
				continue
			}
			rec.Size = info.Size()
			rec.Start = info.ModTime()
			// 首行元数据
			if f, err := os.Open(filepath.Join(dir, name)); err == nil {
				sc := bufio.NewScanner(f)
				if sc.Scan() {
					line := sc.Bytes()
					lineCopy := make([]byte, len(line))
					copy(lineCopy, line)
					switch {
					case strings.HasSuffix(name, ".cast"):
						parseCastMeta(lineCopy, &rec)
					case strings.HasSuffix(name, ".log"):
						rec.Legacy = true
						parseLegacyMeta(lineCopy, &rec)
					}
				}
				_ = f.Close()
			}
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.After(out[j].Start) })
	return out, nil
}

// ReadTerminalCast 读取会话录制为 asciinema v2 文本；旧 .log 即时转单帧
// cast（原始流作为一个输出帧——无时序，整屏呈现）。name 仅允许裸文件名
// （防路径穿越）。
func (s *Service) ReadTerminalCast(serverID uint, name string) (string, error) {
	if name == "" || filepath.Base(name) != name ||
		(!strings.HasSuffix(name, ".cast") && !strings.HasSuffix(name, ".log")) {
		return "", fmt.Errorf("无效的会话文件名")
	}
	path := filepath.Join(terminalAuditDir, fmt.Sprintf("%d", serverID), name)
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取会话文件失败: %w", err)
	}
	if strings.HasSuffix(name, ".cast") {
		return string(raw), nil
	}
	// 旧 .log：首行元数据 + 余下原始流 → 单帧 cast
	nl := indexByte(raw, '\n')
	meta, body := raw, []byte{}
	if nl >= 0 {
		meta, body = raw[:nl], raw[nl+1:]
	}
	rec := TerminalSessionOut{}
	metaCopy := make([]byte, len(meta))
	copy(metaCopy, meta)
	parseLegacyMeta(metaCopy, &rec)
	if rec.ServerID == 0 {
		rec.ServerID = serverID
	}
	startUnix := int64(0)
	if !rec.Start.IsZero() {
		startUnix = rec.Start.Unix()
	}
	header, _ := json.Marshal(map[string]any{
		"version": 2, "width": ptyCols, "height": ptyRows,
		"timestamp": startUnix,
		"title":     fmt.Sprintf("%s@%s（旧格式：无时序）", rec.Operator, rec.Server),
		"env":       map[string]string{"TERM": "xterm-256color", "operator": rec.Operator},
	})
	frame, _ := json.Marshal([]any{0.0, "o", string(body)})
	return string(header) + "\n" + string(frame) + "\n", nil
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}
