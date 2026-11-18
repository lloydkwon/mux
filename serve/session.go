package serve

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

const (
	// sessionCookie 는 로그인 쿠키다. __Host- 접두사는 브라우저가 Secure·Path=/·Domain 없음일 때만 받게 한다.
	sessionCookie = "__Host-muxtap"
	// sessionIdle 동안 요청이 없으면 다시 Face ID 를 묻는다(사용자 결정, 2026-11-18). 화면이 열려 있으면 2초마다
	// 목록을 읽으니 끊기지 않고, 폰을 잠그면 요청이 멈춰 세기 시작한다.
	sessionIdle = 30 * time.Minute
	// maxSessions 를 넘으면 가장 오래 쉰 것부터 버린다 — 로그인을 되풀이해도 메모리가 자라지 않게.
	maxSessions = 32
	// ceremonyTTL 은 Face ID 창을 띄운 뒤 끝내기까지 기다리는 시간이다.
	ceremonyTTL = 3 * time.Minute
	// maxCeremonies 를 넘으면 가장 오래된 것부터 버린다.
	maxCeremonies = 16
)

// authSession 은 로그인한 기기 하나다. 메모리에만 있다 — 서버를 다시 띄우면 모두 다시 Face ID 다.
type authSession struct {
	login    string
	lastSeen time.Time
}

// ceremony 는 시작한 등록·로그인 하나다. WebAuthn 의 도전값(SessionData)은 서버에만 두고, 폰에는 id 만 준다.
type ceremony struct {
	register bool
	login    string // 시작한 Tailscale 로그인 — 끝내는 요청도 같은 로그인이어야 한다
	data     webauthn.SessionData
	codeHash string // 등록일 때 맞힌 코드
	started  time.Time
}

// sessionTable 은 로그인 쿠키와 진행 중인 의식을 들고 있다.
type sessionTable struct {
	mu         sync.Mutex
	sessions   map[string]authSession
	ceremonies map[string]ceremony
}

func newSessionTable() *sessionTable {
	return &sessionTable{sessions: map[string]authSession{}, ceremonies: map[string]ceremony{}}
}

func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b) // crypto/rand.Read 는 실패하지 않는다(Go 1.24+ 는 실패하면 프로그램을 멈춘다)
	return base64.RawURLEncoding.EncodeToString(b)
}

// open 은 새 로그인을 만들고 쿠키에 넣을 값을 돌려준다.
func (t *sessionTable) open(login string, now time.Time) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	for len(t.sessions) >= maxSessions {
		oldest := ""
		for k, s := range t.sessions {
			if oldest == "" || s.lastSeen.Before(t.sessions[oldest].lastSeen) {
				oldest = k
			}
		}
		delete(t.sessions, oldest)
	}
	token := randomToken()
	t.sessions[token] = authSession{login: login, lastSeen: now}
	return token
}

// touch 는 쿠키가 살아 있는 로그인이고 같은 Tailscale 로그인의 것이면 true 이고, 쉰 시간을 0으로 돌린다.
// 오래 쉰 것은 지운다.
func (t *sessionTable) touch(token, login string, now time.Time) bool {
	if token == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.sessions[token]
	if !ok {
		return false
	}
	if now.Sub(s.lastSeen) > sessionIdle {
		delete(t.sessions, token)
		return false
	}
	if s.login != login {
		return false
	}
	s.lastSeen = now
	t.sessions[token] = s
	return true
}

// begin 은 의식을 적고 id 를 돌려준다.
func (t *sessionTable) begin(c ceremony) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, old := range t.ceremonies {
		if c.started.Sub(old.started) > ceremonyTTL {
			delete(t.ceremonies, k)
		}
	}
	for len(t.ceremonies) >= maxCeremonies {
		oldest := ""
		for k, old := range t.ceremonies {
			if oldest == "" || old.started.Before(t.ceremonies[oldest].started) {
				oldest = k
			}
		}
		delete(t.ceremonies, oldest)
	}
	id := randomToken()
	t.ceremonies[id] = c
	return id
}

// finish 는 의식을 꺼내고 지운다 — 한 도전값은 한 번만 쓴다. 없거나, 시간이 지났거나, 종류·로그인이 다르면 false.
func (t *sessionTable) finish(id string, register bool, login string, now time.Time) (ceremony, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	c, ok := t.ceremonies[id]
	if !ok {
		return ceremony{}, false
	}
	delete(t.ceremonies, id)
	if c.register != register || c.login != login || now.Sub(c.started) > ceremonyTTL {
		return ceremony{}, false
	}
	return c, true
}

// sessionToken 은 요청의 로그인 쿠키 값이다.
func sessionToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}
