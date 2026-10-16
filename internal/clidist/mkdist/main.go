// mkdist 交叉编译各平台的 cohive，压缩后写进 internal/clidist/files，并写清单，供服务端嵌入。
// 由 make cli-dist 调用：go run ./internal/clidist/mkdist -out internal/clidist/files -version … -commit … -ldflags …
package main

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cohive-ai/cohive/internal/clidist"
)

func main() {
	out := flag.String("out", "internal/clidist/files", "输出目录")
	version := flag.String("version", "dev", "版本号")
	commit := flag.String("commit", "", "提交号")
	ldflags := flag.String("ldflags", "", "传给 go build 的 -ldflags")
	pkg := flag.String("pkg", "./cmd/cohive", "要构建的包")
	flag.Parse()
	if err := run(*out, *version, *commit, *ldflags, *pkg); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}

func run(out, version, commit, ldflags, pkg string) error {
	// 清掉上一次的产物，只保留占位文件
	entries, err := os.ReadDir(out)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() != ".gitkeep" {
			if err := os.RemoveAll(filepath.Join(out, e.Name())); err != nil {
				return err
			}
		}
	}
	tmp, err := os.MkdirTemp("", "cohive-dist")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	m := clidist.Manifest{Version: version, Commit: commit, Files: map[string]clidist.File{}}
	for _, p := range clidist.Platforms {
		goos, goarch, _ := strings.Cut(p, "-")
		name := "cohive"
		if goos == "windows" {
			name = "cohive.exe"
		}
		bin := filepath.Join(tmp, p, name)
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", bin, pkg)
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("构建 %s：%w", p, err)
		}
		f, err := pack(bin, filepath.Join(out, "cohive-"+p+".gz"))
		if err != nil {
			return fmt.Errorf("压缩 %s：%w", p, err)
		}
		f.Name = name
		m.Files[p] = f
		fmt.Fprintf(os.Stderr, "  %s  %.1f MB  %s\n", p, float64(f.Size)/(1<<20), f.SHA256)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, clidist.ManifestName), append(b, '\n'), 0o644)
}

// pack 把 src 用 gzip 最高压缩率写到 dst（不写文件名与时间，产物可复现），返回原文件的大小与校验和。
func pack(src, dst string) (clidist.File, error) {
	in, err := os.Open(src)
	if err != nil {
		return clidist.File{}, err
	}
	defer in.Close()
	outF, err := os.Create(dst)
	if err != nil {
		return clidist.File{}, err
	}
	defer outF.Close()
	zw, err := gzip.NewWriterLevel(outF, gzip.BestCompression)
	if err != nil {
		return clidist.File{}, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(zw, h), in)
	if err != nil {
		return clidist.File{}, err
	}
	if err := zw.Close(); err != nil {
		return clidist.File{}, err
	}
	return clidist.File{GZ: filepath.Base(dst), Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, outF.Close()
}
