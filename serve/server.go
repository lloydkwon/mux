// Package serve 는 `mux serve` — 폰 브라우저용 원격 화면이다(docs/design/serve.md).
//
// Tailscale 의 `tailscale serve` 뒤에서만 돈다: 127.0.0.1 에만 바인드하고, tailscale 이 붙이는 신원 헤더가
// 허용 목록과 같을 때만 응답한다. 세션 목록(패널과 같은 판정), Claude 에게 보내기(mux agent prompt 와 같은
// 절차), 마지막 턴과 화면 읽기만 한다. 접속한 클라이언트가 없으면 아무것도 돌지 않는다 — 틱도 감시도 없다.
package serve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultHostSuffix 는 Host 이름이 끝나야 하는 꼬리다. tailscale serve 가 넘기는 이름은 <기계>.<tailnet>.ts.net 이다.
const DefaultHostSuffix = ".ts.net"

// Config 는 mux serve 의 설정이다. 모두 플래그에서 온다.
type Config struct {
	// Addr 는 바인드 주소다. 루프백만 받는다 — tailnet·LAN 에 직접 열리면 신원 헤더를 아무나 붙일 수 있다.
	Addr string
	// Allow 는 들어올 수 있는 Tailscale 로그인(Tailscale-User-Login)이다. 비면 시작하지 않는다.
	Allow []string
	// HostSuffix 는 Host 이름이 끝나야 하는 꼬리다(DNS 리바인딩 막기). 비면 DefaultHostSuffix.
	HostSuffix string
}

// normalized 는 설정을 검사하고 빈 값을 채운 사본을 돌려준다.
func (c Config) normalized() (Config, error) {
	host, _, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return Config{}, fmt.Errorf("--addr %q: %w", c.Addr, err)
	}
	if !isLoopback(host) {
		return Config{}, fmt.Errorf("--addr %q: 루프백(127.0.0.1)에만 바인드한다 — 폰에서는 tailscale serve 로 연다", c.Addr)
	}
	allow := make([]string, 0, len(c.Allow))
	for _, a := range c.Allow {
		a = strings.TrimSpace(a)
		if a == "" {
			return Config{}, errors.New("--allow 에 빈 로그인이 있다")
		}
		allow = append(allow, a)
	}
	if len(allow) == 0 {
		return Config{}, errors.New("--allow 로 들어올 Tailscale 로그인을 하나 이상 적는다 (예: --allow you@example.com, `tailscale whois <내 tailnet IP>` 의 User)")
	}
	suffix := strings.ToLower(strings.TrimSpace(c.HostSuffix))
	if suffix == "" {
		suffix = DefaultHostSuffix
	}
	if !strings.HasPrefix(suffix, ".") {
		suffix = "." + suffix
	}
	return Config{Addr: c.Addr, Allow: allow, HostSuffix: suffix}, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Run 은 ctx 가 끝날 때까지 서버를 돌린다. logOut 에는 시작·거절·보내기 결과를 한 줄씩 적는다 — 보낸 글은
// 적지 않는다(프롬프트는 붙여 넣은 무엇이든 담는다).
func Run(ctx context.Context, cfg Config, logOut io.Writer) error {
	cfg, err := cfg.normalized()
	if err != nil {
		return err
	}
	logger := log.New(logOut, "mux serve: ", log.LstdFlags)
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Addr, err)
	}
	srv := &http.Server{
		Handler:           newHandler(cfg, tmuxBackend{}, logger),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		// 보내기는 제출 확인까지 십수 초, 대화 읽기는 늦게 쓰이는 기록을 16초까지 기다린다.
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  2 * time.Minute,
		ErrorLog:     logger,
	}
	logger.Printf("http://%s 에서 듣는다 (허용: %s). 폰에서 열려면 한 번: tailscale serve --bg --https=8443 %s",
		ln.Addr(), strings.Join(cfg.Allow, ", "), ln.Addr())

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// server 는 핸들러들이 나눠 쓰는 것이다.
type server struct {
	cfg Config
	b   backend
	log *log.Logger
	// listMu 는 목록 계산을 줄 세운다. 탭 두 개가 같은 순간 물어도 pgrep·ps·git 을 두 번 돌리지 않게 —
	// 뒤에 온 쪽은 앞의 것이 채운 TTL 캐시를 읽는다.
	listMu sync.Mutex
}

func newHandler(cfg Config, b backend, logger *log.Logger) http.Handler {
	s := &server{cfg: cfg, b: b, log: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", serveStatic("index.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /app.js", serveStatic("app.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /app.css", serveStatic("app.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /api/list", s.handleList)
	mux.HandleFunc("POST /api/prompt", s.handlePrompt)
	mux.HandleFunc("GET /api/turn", s.handleTurn)
	mux.HandleFunc("GET /api/screen", s.handleScreen)
	return s.guard(mux)
}
