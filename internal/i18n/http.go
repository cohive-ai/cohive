package i18n

import "net/http"

// FromRequest 按请求头 Accept-Language 匹配语言，匹配不到时用本进程的缺省语言（服务器缺省语言）。
func FromRequest(r *http.Request) Lang {
	if l, ok := Match(r.Header.Get("Accept-Language")); ok {
		return l
	}
	return Default()
}

// Middleware 把请求的语言放进上下文，并在响应头 Content-Language 里回告。
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := FromRequest(r)
		w.Header().Set("Content-Language", string(l))
		next.ServeHTTP(w, r.WithContext(WithLang(r.Context(), l)))
	})
}

// FromResponse 读取 Middleware 写在响应头里的语言，供只拿得到 ResponseWriter 的地方使用。
func FromResponse(w http.ResponseWriter) Lang {
	if l, ok := Match(w.Header().Get("Content-Language")); ok {
		return l
	}
	return Default()
}
