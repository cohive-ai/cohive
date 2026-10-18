package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

// call 发一次请求，返回状态码、响应头 Content-Language 与报错文字。
func call(t *testing.T, method, url, token, accept string, in any) (int, string, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	if in != nil {
		_ = json.NewEncoder(&body).Encode(in)
	}
	req, _ := http.NewRequest(method, url, &body)
	req.Header.Set("Authorization", "Bearer "+token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if accept != "" {
		req.Header.Set("Accept-Language", accept)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, resp.Header.Get("Content-Language"), out
}

// 账号语言：没带 Accept-Language 的请求（命令行没设 COHIVE_LANG）按账号语言回报错并回告；带了以请求头为准；
// 账号语言为空时用服务器缺省语言。/me 返回账号语言、服务器缺省语言与可选语言。
func TestAccountLanguage(t *testing.T) {
	f := newFixture(t, builtWeb)
	me := f.url + "/api/v1/me"
	bad := f.url + "/api/v1/w/default/issues/abc"

	code, lang, out := call(t, "GET", me, f.tokenA, "", nil)
	acc := out["account"].(map[string]any)
	if code != 200 || lang != "zh-CN" || acc["language"] != "" || out["default_language"] != "zh-CN" || len(out["languages"].([]any)) < 2 {
		t.Fatalf("初始：%d %q %v", code, lang, out)
	}
	if code, _, out := call(t, "POST", f.url+"/api/v1/me/language", f.tokenA, "", map[string]string{"language": "en-GB"}); code != 200 || out["language"] != "en" {
		t.Fatalf("设为英文：%d %v", code, out)
	}
	if code, lang, out := call(t, "GET", bad, f.tokenA, "", nil); code != 400 || lang != "en" || out["error"] != "Invalid issue ID" {
		t.Fatalf("没带语言头时按账号语言：%d %q %v", code, lang, out)
	}
	if code, lang, out := call(t, "GET", bad, f.tokenA, "zh-CN", nil); code != 400 || lang != "zh-CN" || out["error"] == "Invalid issue ID" {
		t.Fatalf("带了语言头以请求头为准：%d %q %v", code, lang, out)
	}
	if code, _, out := call(t, "GET", me, f.tokenA, "", nil); code != 200 || out["account"].(map[string]any)["language"] != "en" {
		t.Fatalf("/me 应返回账号语言：%v", out)
	}
	if code, _, out := call(t, "POST", f.url+"/api/v1/me/language", f.tokenA, "en", map[string]string{"language": "tlh"}); code != 400 || out["code"] != "invalid_input" || out["error"] != "Unsupported language tlh" {
		t.Fatalf("不认识的语言：%d %v", code, out)
	}
	if code, _, _ := call(t, "POST", f.url+"/api/v1/me/language", f.tokenA, "", map[string]string{"language": ""}); code != 200 {
		t.Fatal("清除账号语言失败")
	}
	if _, lang, _ := call(t, "GET", bad, f.tokenA, "", nil); lang != "zh-CN" {
		t.Fatalf("清除后跟随服务器缺省语言：%q", lang)
	}
	// 别人的账号语言不受影响
	if _, lang, _ := call(t, "GET", bad, f.tokenB, "", nil); lang != "zh-CN" {
		t.Fatalf("lee-codex 不受影响：%q", lang)
	}
}
