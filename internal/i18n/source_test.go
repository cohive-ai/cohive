package i18n

import (
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

var repoRoot = filepath.Join("..", "..")

// goSources 返回仓库里全部非测试 Go 文件（相对仓库根目录、用 / 分隔）。
func goSources(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(repoRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(repoRoot, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch {
			case rel != "." && strings.HasPrefix(d.Name(), "."), d.Name() == "node_modules", d.Name() == "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go") {
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestKeysUsedExist：代码里以字面量写出的文案键（i18n.T、Lang.T、i18n.Errorf、i18n.Wrap）都必须在英文语言包里。
func TestKeysUsedExist(t *testing.T) {
	fset := token.NewFileSet()
	for _, rel := range goSources(t) {
		f, err := parser.ParseFile(fset, filepath.Join(repoRoot, rel), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, _ := sel.X.(*ast.Ident)
			arg := -1
			switch {
			case sel.Sel.Name == "T":
				arg = 0
			case pkg != nil && pkg.Name == "i18n" && sel.Sel.Name == "Errorf":
				arg = 0
			case pkg != nil && pkg.Name == "i18n" && sel.Sel.Name == "Wrap":
				arg = 1
			}
			if arg < 0 || len(call.Args) <= arg {
				return true
			}
			lit, ok := call.Args[arg].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			key, _ := strconv.Unquote(lit.Value)
			if keyPattern.MatchString(key) && !EN.Has(key) {
				t.Errorf("%s: key %q is not in internal/i18n/locales/en", fset.Position(lit.Pos()), key)
			}
			return true
		})
	}
}

// stage1Pending 是第一段暂不改动的目录（在另一条分支上），第二段清空。
var stage1Pending = []string{
	"internal/core/",
	"internal/notify/",
	"internal/store/",
}

// guardExempt 永久不检查：测试辅助代码只给开发者看。
var guardExempt = []string{
	"internal/testutil/",
}

func hasCJK(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) || (r >= 0x3000 && r <= 0x303f) || (r >= 0xff00 && r <= 0xffef) {
			return true
		}
	}
	return false
}

// TestNoHardcodedChinese：非测试 Go 代码的字符串字面量里不许直接写中文，文案一律进语言包。
// 确有必要的行（例如解析用户输入的中文指令）在同一行写注释 i18n-ignore。
func TestNoHardcodedChinese(t *testing.T) {
	for _, rel := range goSources(t) {
		if hasPrefix(rel, stage1Pending) || hasPrefix(rel, guardExempt) {
			continue
		}
		src, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		file := fset.AddFile(rel, -1, len(src))
		var s scanner.Scanner
		s.Init(file, src, nil, scanner.ScanComments)
		ignored := map[int]bool{}
		var hits []token.Position
		for {
			pos, tok, lit := s.Scan()
			if tok == token.EOF {
				break
			}
			switch tok {
			case token.COMMENT:
				if strings.Contains(lit, "i18n-ignore") {
					ignored[fset.Position(pos).Line] = true
				}
			case token.STRING, token.CHAR:
				if hasCJK(lit) {
					hits = append(hits, fset.Position(pos))
				}
			}
		}
		for _, p := range hits {
			if !ignored[p.Line] {
				t.Errorf("%s: hard-coded Chinese text; move it to internal/i18n/locales (en and zh-CN)", p)
			}
		}
	}
}

func hasPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
