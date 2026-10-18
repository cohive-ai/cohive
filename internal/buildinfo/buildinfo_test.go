package buildinfo

import "testing"

// 版本字符串与语言无关，形如 v1.2.3 (abc1234)；没有提交号时只有版本。
func TestString(t *testing.T) {
	oldV, oldC := Version, Commit
	defer func() { Version, Commit = oldV, oldC }()
	Version, Commit = "v1.2.3", "abc1234"
	if got := String(); got != "v1.2.3 (abc1234)" {
		t.Errorf("得到 %q", got)
	}
	Commit = ""
	if got := String(); got != "v1.2.3" {
		t.Errorf("没有提交号：%q", got)
	}
}
