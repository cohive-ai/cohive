// Package clidist 让协作服务自己提供成员本机命令行 cohive 的下载与安装脚本。
//
// 五个平台的 cohive 在构建服务端时一并交叉编译、压缩后嵌入（make cli-dist，见 mkdist），
// 与服务端同一次构建产出，版本天然一致；同事不必装 Go，也不必能读代码仓库。
// 没有执行 make cli-dist 的构建（go build、go test）只含占位文件，接口与安装脚本会给出明确提示。
package clidist

import (
	"compress/gzip"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"text/template"
)

//go:embed all:files
var embedded embed.FS

// Platforms 是提供安装包的平台，顺序固定。
var Platforms = []string{"darwin-arm64", "darwin-amd64", "linux-amd64", "linux-arm64", "windows-amd64"}

// ManifestName 是清单文件名。
const ManifestName = "manifest.json"

// File 是一个平台的安装包。
type File struct {
	Name   string `json:"name"`   // 安装后的文件名：cohive 或 cohive.exe
	GZ     string `json:"gz"`     // 嵌入的压缩文件名
	Size   int64  `json:"size"`   // 解压后的字节数
	SHA256 string `json:"sha256"` // 解压后的 SHA-256，十六进制小写
}

// Manifest 是 make cli-dist 写下的清单。
type Manifest struct {
	Version string          `json:"version"`
	Commit  string          `json:"commit"`
	Files   map[string]File `json:"files"`
}

// Dist 是一套可下载的安装包；Manifest 为 nil 表示这次构建没有附带安装包。
type Dist struct {
	fsys     fs.FS
	Manifest *Manifest
}

// Embedded 返回随程序嵌入的安装包。
func Embedded() (*Dist, error) {
	sub, err := fs.Sub(embedded, "files")
	if err != nil {
		return nil, err
	}
	return New(sub)
}

var (
	sha256Hex   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	safeVersion = regexp.MustCompile(`^[0-9A-Za-z._+-]{1,64}$`)
)

// New 从 fsys 读清单并校验；没有清单时返回不带安装包的 Dist。
func New(fsys fs.FS) (*Dist, error) {
	b, err := fs.ReadFile(fsys, ManifestName)
	if errors.Is(err, fs.ErrNotExist) {
		return &Dist{fsys: fsys}, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("命令行安装包清单格式不对：%w", err)
	}
	// 版本号会写进安装脚本，只接受常见的版本字符
	if !safeVersion.MatchString(m.Version) {
		return nil, fmt.Errorf("命令行安装包清单里的版本号 %q 不合法", m.Version)
	}
	for p, f := range m.Files {
		if !known(p) {
			return nil, fmt.Errorf("命令行安装包清单里有未知平台 %q", p)
		}
		if !sha256Hex.MatchString(f.SHA256) || f.Size <= 0 || f.GZ == "" || strings.ContainsAny(f.GZ, `/\`) {
			return nil, fmt.Errorf("命令行安装包清单里 %s 的条目不完整", p)
		}
		if _, err := fs.Stat(fsys, f.GZ); err != nil {
			return nil, fmt.Errorf("命令行安装包缺少 %s：%w", f.GZ, err)
		}
	}
	return &Dist{fsys: fsys, Manifest: &m}, nil
}

func known(p string) bool {
	for _, x := range Platforms {
		if x == p {
			return true
		}
	}
	return false
}

// Available 返回清单里有的平台，按 Platforms 的顺序。
func (d *Dist) Available() []string {
	if d.Manifest == nil {
		return nil
	}
	var out []string
	for _, p := range Platforms {
		if _, ok := d.Manifest.Files[p]; ok {
			out = append(out, p)
		}
	}
	return out
}

// Summary 是启动时打印的一行说明。
func (d *Dist) Summary() string {
	if d.Manifest == nil {
		return "未附带命令行安装包（构建时没有执行 make cli-dist），/install.sh 会提示联系管理员"
	}
	return fmt.Sprintf("命令行安装包：版本 %s，%d 个平台（%s）", d.Manifest.Version, len(d.Available()), strings.Join(d.Available(), "、"))
}

const noDist = "这台服务器没有附带命令行安装包（构建服务端时没有执行 make cli-dist），请联系管理员"

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// validHost 只接受由字母、数字、点、连字符、冒号、方括号组成的 Host（域名、IPv4、IPv6 加端口）。
// 服务地址会写进安装脚本，限制字符集就不存在往脚本里注入命令的可能。
var validHost = regexp.MustCompile(`^[A-Za-z0-9.\-]+(?::[0-9]{1,5})?$|^\[[0-9A-Fa-f:.]+\](?::[0-9]{1,5})?$`)

// Register 在 mux 上注册安装脚本与下载接口。origin 返回用户访问本站用的地址（scheme://host）。
// serverVersion 是服务端自己的版本，供 /api/v1/version 返回。
func (d *Dist) Register(mux *http.ServeMux, origin func(*http.Request) string, serverVersion, serverCommit string) {
	mux.HandleFunc("GET /install.sh", func(w http.ResponseWriter, r *http.Request) {
		d.serveScript(w, r, origin, shTemplate)
	})
	mux.HandleFunc("GET /install.ps1", func(w http.ResponseWriter, r *http.Request) {
		d.serveScript(w, r, origin, psTemplate)
	})
	mux.HandleFunc("GET /dl/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "没有这个文件")
	})
	mux.HandleFunc("GET /dl/cohive/{platform}", func(w http.ResponseWriter, r *http.Request) {
		p := r.PathValue("platform")
		if d.Manifest == nil {
			writeErr(w, http.StatusNotFound, noDist)
			return
		}
		if p == "SHA256SUMS" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			for _, pl := range d.Available() {
				fmt.Fprintf(w, "%s  cohive-%s\n", d.Manifest.Files[pl].SHA256, pl)
			}
			return
		}
		f, ok := d.Manifest.Files[p]
		if !ok {
			writeErr(w, http.StatusNotFound, fmt.Sprintf("没有 %s 的安装包；提供的平台：%s", p, strings.Join(d.Available(), "、")))
			return
		}
		src, err := d.fsys.Open(f.GZ)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "读取安装包失败")
			return
		}
		defer src.Close()
		zr, err := gzip.NewReader(src)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "读取安装包失败")
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(f.Size, 10))
		w.Header().Set("Content-Disposition", `attachment; filename="`+f.Name+`"`)
		w.Header().Set("Cache-Control", "no-cache")
		if r.Method == http.MethodHead {
			return
		}
		_, _ = io.CopyN(w, zr, f.Size)
	})
	mux.HandleFunc("GET /api/v1/version", func(w http.ResponseWriter, r *http.Request) {
		out := map[string]any{"version": serverVersion, "commit": serverCommit, "cli": nil}
		if d.Manifest != nil {
			out["cli"] = map[string]any{"version": d.Manifest.Version, "commit": d.Manifest.Commit, "platforms": d.Available()}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(out)
	})
}

type scriptData struct {
	Server, Version string
	Files           []platformSum
	Windows         string // windows-amd64 的校验和；没有时为空
	Missing         string // 没有安装包时的说明
}

type platformSum struct{ Platform, SHA256 string }

func (d *Dist) serveScript(w http.ResponseWriter, r *http.Request, origin func(*http.Request) string, t *template.Template) {
	if !validHost.MatchString(r.Host) {
		http.Error(w, "请求的 Host 不合法", http.StatusBadRequest)
		return
	}
	data := scriptData{Server: origin(r)}
	if d.Manifest == nil {
		data.Missing = noDist
	} else {
		data.Version = d.Manifest.Version
		for _, p := range d.Available() {
			data.Files = append(data.Files, platformSum{p, d.Manifest.Files[p].SHA256})
		}
		if f, ok := d.Manifest.Files["windows-amd64"]; ok {
			data.Windows = f.SHA256
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if err := t.Execute(w, data); err != nil {
		http.Error(w, "生成安装脚本失败", http.StatusInternalServerError)
	}
}

//go:embed install.sh.tmpl
var shSource string

//go:embed install.ps1.tmpl
var psSource string

var (
	shTemplate = template.Must(template.New("install.sh").Parse(shSource))
	psTemplate = template.Must(template.New("install.ps1").Parse(psSource))
)
