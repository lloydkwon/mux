package serve

import (
	"mime"
	"net"
	"net/http"
	"strings"
)

// 신원은 tailscale serve 가 붙이는 헤더 하나다. tailscale 은 들어온 같은 이름의 헤더를 지우고 자기 값을
// 넣는다 — 이것이 지켜지는지는 폰 실측 전에 잰다(serve.md «실측할 것» 1).
const (
	headerLogin  = "Tailscale-User-Login"
	headerFunnel = "Tailscale-Funnel-Request"
)

// securityHeaders 는 모든 응답(거절 포함)에 단다. 인라인 스크립트가 없으므로 script-src 'self' 가 지켜진다.
// frame-ancestors 'none' 은 다른 사이트가 이 페이지를 iframe 에 넣고 [보내기] 를 누르게 만드는 것을 막는다.
// JS 가 CSSOM(element.style)으로 칠하는 색은 style-src 에 걸리지 않는다.
var securityHeaders = map[string]string{
	"Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
		"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'",
	"X-Content-Type-Options": "nosniff",
	"X-Frame-Options":        "DENY",
	"Referrer-Policy":        "no-referrer",
	"Permissions-Policy":     "microphone=(self), camera=(), geolocation=()",
}

// guard 는 모든 요청 앞에 선다. 거절의 이유는 서버 로그에만 적고, 응답에는 고칠 방법만 말한다.
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range securityHeaders {
			w.Header().Set(k, v)
		}
		if status, why := s.refuse(r); status != 0 {
			s.log.Printf("거절 %s %s (%d): %s", r.Method, r.URL.Path, status, why)
			http.Error(w, "tailscale serve 를 거친 허용된 사용자만 열 수 있습니다.", status)
			return
		}
		// 둘째 겹: 패스키 로그인(login.go). 로그인 전에는 로그인 화면과 그 재료만 열린다. 로그에는 적지 않는다 —
		// 쉬다 돌아온 폰이 한 번씩 받는 평범한 답이다.
		if s.auth != nil && !isPublic(r.URL.Path) && !s.auth.loggedIn(r) {
			if r.Method == http.MethodGet && r.URL.Path == "/" {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			writeError(w, http.StatusUnauthorized, "login")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// refuse 는 요청을 거절할 이유를 돌려준다. 받으면 0.
func (s *server) refuse(r *http.Request) (int, string) {
	// funnel 은 쓰지 않는다. 누가 켜도 인터넷에서 온 요청은 닿지 않게.
	if r.Header.Get(headerFunnel) != "" {
		return http.StatusForbidden, "funnel 요청"
	}
	// 루프백 포트는 이 기계의 브라우저에서도 닿는다. DNS 리바인딩으로 남의 이름을 127.0.0.1 로 바꾼 페이지는
	// 같은 출처로 헤더를 마음대로 붙일 수 있다 — Host 가 tailnet 이름일 때만 받는다.
	if !strings.HasSuffix(strings.ToLower(hostname(r.Host)), s.cfg.HostSuffix) {
		return http.StatusForbidden, "Host " + r.Host
	}
	login := r.Header.Get(headerLogin)
	if login == "" {
		return http.StatusForbidden, headerLogin + " 이 없다 — tailscale serve 를 거치지 않은 요청"
	}
	if !s.allowed(login) {
		return http.StatusForbidden, "허용 목록 밖의 로그인 " + login
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return 0, ""
	}
	// 쓰는 요청은 같은 출처에서 JSON 으로만 — JSON 본문은 다른 출처에서 사전 요청 없이 보낼 수 없다.
	if origin := r.Header.Get("Origin"); origin != "https://"+r.Host {
		return http.StatusForbidden, "Origin " + origin
	}
	if ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || !acceptsBody(r.URL.Path, ct) {
		return http.StatusUnsupportedMediaType, "Content-Type " + r.Header.Get("Content-Type")
	}
	return 0, ""
}

// acceptsBody 는 그 경로가 받는 본문 형식인가다. 받아쓰기만 녹음(audio/webm·audio/mp4)을 받는다 — 둘 다 JSON 처럼
// CORS 의 단순 요청 형식이 아니다. 단순 요청 형식(multipart/form-data·text/plain·form)은 어느 경로도 받지 않는다.
func acceptsBody(path, contentType string) bool {
	if path == "/api/transcribe" {
		_, ok := audioTypes[contentType]
		return ok
	}
	return contentType == "application/json"
}

func (s *server) allowed(login string) bool {
	for _, a := range s.cfg.Allow {
		if strings.EqualFold(a, login) {
			return true
		}
	}
	return false
}

// hostname 은 Host 에서 포트를 뗀 이름이다.
func hostname(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// requestLogin 은 guard 를 지난 요청의 로그인이다(로그용).
func requestLogin(r *http.Request) string {
	return r.Header.Get(headerLogin)
}
