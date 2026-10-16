package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

// Proxy 把编程工具通过标准输入输出发来的 MCP 消息（每行一条 JSON-RPC）转发到协作服务的 /mcp，
// 并把响应写回标准输出。令牌只在本进程内使用，不出现在项目配置文件里。
type Proxy struct {
	Server string
	Token  string
	HTTP   *http.Client
	Log    io.Writer
	// Unavailable 非空时不转发，对每个请求都返回这条提示（例如本机还没登录）。
	Unavailable string
	// Refresh 在服务端返回 401 时换一枚新令牌（用设备密钥重签）；为空时不重试。
	// 每个进程只换一次：重签也失败就报错，不循环。
	Refresh func(ctx context.Context) (string, error)

	outMu      sync.Mutex
	stateMu    sync.Mutex
	refreshed  bool   // 已用 Refresh 换过令牌
	refreshErr error  // 换令牌失败的原因，告诉用户
	protocol   string // 初始化时协商的协议版本
	session    string // 服务端若返回会话编号则带上
}

type rpcHead struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
}

// Run 一直转发到输入结束。每条请求并发处理，初始化请求先处理完再处理后续消息。
func (p *Proxy) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	if p.HTTP == nil {
		// 不设总超时：工具调用和流式响应可能较久；连接阶段由默认传输层的超时兜底
		p.HTTP = &http.Client{}
	}
	r := bufio.NewReaderSize(in, 1<<20)
	var wg sync.WaitGroup
	for {
		line, err := r.ReadBytes('\n')
		if msg := bytes.TrimSpace(line); len(msg) > 0 {
			var head rpcHead
			if err := json.Unmarshal(msg, &head); err != nil {
				p.write(out, []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"解析错误：输入不是合法的 JSON"}}`))
				continue
			}
			if head.Method == "initialize" {
				p.forward(ctx, msg, head, out)
			} else {
				wg.Add(1)
				go func(msg []byte) {
					defer wg.Done()
					p.forward(ctx, msg, head, out)
				}(append([]byte(nil), msg...))
			}
		}
		if errors.Is(err, io.EOF) {
			wg.Wait()
			return nil
		}
		if err != nil {
			wg.Wait()
			return err
		}
	}
}

func (p *Proxy) logf(format string, args ...any) {
	if p.Log != nil {
		fmt.Fprintf(p.Log, "cohive mcp-proxy: "+format+"\n", args...)
	}
}

func (p *Proxy) write(out io.Writer, msg []byte) bool {
	var buf bytes.Buffer
	if json.Compact(&buf, msg) != nil {
		return false
	}
	buf.WriteByte('\n')
	p.outMu.Lock()
	defer p.outMu.Unlock()
	_, _ = out.Write(buf.Bytes())
	return true
}

// fail 对带编号的请求返回 JSON-RPC 错误；通知没有编号，不需要回复。
func (p *Proxy) fail(out io.Writer, head rpcHead, msg string) {
	p.logf("%s", msg)
	if len(head.ID) == 0 || string(head.ID) == "null" {
		return
	}
	resp, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": head.ID,
		"error": map[string]any{"code": -32000, "message": msg},
	})
	p.write(out, resp)
}

// token 返回当前令牌。
func (p *Proxy) token() string {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	return p.Token
}

// renew 在令牌被拒后换一枚新令牌：别的请求已经换过就直接用新的；本进程只调用一次 Refresh。
func (p *Proxy) renew(ctx context.Context, used string) bool {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	if p.Token != used {
		return true
	}
	if p.Refresh == nil || p.refreshed {
		return false
	}
	p.refreshed = true
	tok, err := p.Refresh(ctx)
	if err != nil {
		p.logf("重新签发令牌失败：%v", err)
		p.refreshErr = err
		return false
	}
	p.logf("令牌已失效，已用这台电脑的设备密钥重新签发")
	p.Token = tok
	return true
}

func (p *Proxy) forward(ctx context.Context, msg []byte, head rpcHead, out io.Writer) {
	if p.Unavailable != "" {
		p.fail(out, head, p.Unavailable)
		return
	}
	resp, err := p.send(ctx, msg)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		used := resp.Request.Header.Get("Authorization")
		resp.Body.Close()
		if p.renew(ctx, strings.TrimPrefix(used, "Bearer ")) {
			resp, err = p.send(ctx, msg)
		} else {
			msg := "Cohive 令牌无效或已吊销：如果你已被移出这个工作空间，请联系管理员；否则请执行 cohive login --force 重新配对这台电脑"
			p.stateMu.Lock()
			switch {
			case errors.Is(p.refreshErr, ErrNotMember):
				msg = "Cohive 令牌已失效：你已不是这个工作空间的成员（可能已被移出），请联系它的管理员"
			case p.refreshErr != nil:
				msg = "Cohive 令牌已失效，重新签发也失败：" + p.refreshErr.Error()
			}
			p.stateMu.Unlock()
			p.fail(out, head, msg)
			return
		}
	}
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		p.fail(out, head, fmt.Sprintf("连接 Cohive 协作服务 %s 失败：%v", p.Server, err))
		return
	}
	p.handle(resp, head, out)
}

func (p *Proxy) send(ctx context.Context, msg []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Server+"/mcp", bytes.NewReader(msg))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	p.stateMu.Lock()
	if p.protocol != "" {
		req.Header.Set("Mcp-Protocol-Version", p.protocol)
	}
	if p.session != "" {
		req.Header.Set("Mcp-Session-Id", p.session)
	}
	p.stateMu.Unlock()
	return p.HTTP.Do(req)
}

func (p *Proxy) handle(resp *http.Response, head rpcHead, out io.Writer) {
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		p.stateMu.Lock()
		p.session = sid
		p.stateMu.Unlock()
	}

	switch {
	case resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent:
		return
	case resp.StatusCode == http.StatusUnauthorized:
		p.fail(out, head, "Cohive 令牌无效或已吊销：如果你已被移出这个工作空间，请联系管理员；否则请执行 cohive login --force 重新配对这台电脑")
		return
	case resp.StatusCode >= 300:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		p.fail(out, head, fmt.Sprintf("Cohive 协作服务返回 HTTP %d：%s", resp.StatusCode, p.sanitize(string(body))))
		return
	}

	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		p.relaySSE(resp.Body, head, out)
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		p.fail(out, head, "读取协作服务响应失败")
		return
	}
	p.deliver(body, head, out)
}

// relaySSE 把服务端流式响应里的每个事件数据转成一行输出。
func (p *Proxy) relaySSE(body io.Reader, head rpcHead, out io.Writer) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64<<10), 64<<20)
	var data bytes.Buffer
	flush := func() {
		if data.Len() > 0 {
			p.deliver(data.Bytes(), head, out)
			data.Reset()
		}
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := sc.Err(); err != nil {
		p.fail(out, head, "读取 Cohive 协作服务的流式响应失败："+err.Error())
		return
	}
	flush()
}

func (p *Proxy) deliver(msg []byte, head rpcHead, out io.Writer) {
	if head.Method == "initialize" {
		var r struct {
			Result struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"result"`
		}
		if json.Unmarshal(msg, &r) == nil && r.Result.ProtocolVersion != "" {
			p.stateMu.Lock()
			p.protocol = r.Result.ProtocolVersion
			p.stateMu.Unlock()
		}
	}
	if !p.write(out, msg) {
		p.fail(out, head, "Cohive 协作服务返回了无法识别的内容")
	}
}

var bearerPattern = regexp.MustCompile(`(?i)bearer\s+\S+`)

// sanitize 处理要转给模型看的服务端错误正文：抹掉令牌、截短。
func (p *Proxy) sanitize(body string) string {
	if tok := p.token(); tok != "" {
		body = strings.ReplaceAll(body, tok, "***")
	}
	body = bearerPattern.ReplaceAllString(body, "Bearer ***")
	body = strings.TrimSpace(body)
	if r := []rune(body); len(r) > 300 {
		body = string(r[:300]) + "……"
	}
	return body
}
