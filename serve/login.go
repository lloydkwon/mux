package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// maxAuthBody 는 등록·로그인 요청 본문의 상한이다. 응답은 공개키·서명·인증기 데이터라 몇 KB 다.
const maxAuthBody = 64 << 10

// rpName 은 폰 화면의 이름이다(Muxtap). 패스키 창·홈 화면에 이 이름이 보인다.
const rpName = "Muxtap"

// passkeyAuth 는 패스키 로그인이다. server.auth 가 nil 이면 꺼져 있다(--passkeys 가 빔) — 그때는 Tailscale 신원만 본다.
type passkeyAuth struct {
	store  *passkeyStore
	enroll *enrollGate
	table  *sessionTable
	now    func() time.Time
}

func newPasskeyAuth(passkeys string) *passkeyAuth {
	return &passkeyAuth{
		store:  &passkeyStore{path: passkeys},
		enroll: &enrollGate{path: EnrollPath(passkeys)},
		table:  newSessionTable(),
		now:    time.Now,
	}
}

// publicPaths 는 로그인 없이 열리는 것이다 — 로그인 화면과 그 재료, 홈 화면 아이콘, 등록·로그인 자체. 모두 guard 의
// Tailscale 신원·Host·Origin 검사는 지난 뒤다.
var publicPaths = map[string]bool{
	"/login":                true,
	"/login.js":             true,
	"/app.css":              true,
	"/manifest.webmanifest": true,
	"/icon-180.png":         true,
	"/icon-512.png":         true,
	"/auth/state":           true,
	"/auth/login/begin":     true,
	"/auth/login/finish":    true,
	"/auth/register/begin":  true,
	"/auth/register/finish": true,
}

// isPublic 은 경로가 목록과 꼭 같을 때만 true 다. 앞머리로 맞추지 않는다 — «/auth/../api/list» 가 공개 경로처럼
// 지나가지 않게.
func isPublic(p string) bool {
	return publicPaths[p]
}

// loggedIn 은 요청이 살아 있는 로그인을 가졌는가다. 맞으면 쉰 시간을 0으로 돌린다.
func (a *passkeyAuth) loggedIn(r *http.Request) bool {
	return a.table.touch(sessionToken(r), requestLogin(r), a.now())
}

// relyingParty 는 이 요청의 Host 로 WebAuthn 을 차린다. Host 는 guard 가 이미 tailnet 이름(.ts.net)인지 봤다.
// 패스키는 이 이름(포트 빼고)에 묶이고, 응답의 origin 은 https://<Host> 여야 한다 — ts.net 은 공개 접미사라
// 다른 tailnet 의 페이지가 이 패스키를 쓰지 못한다.
func relyingParty(r *http.Request) (*webauthn.WebAuthn, error) {
	host := strings.ToLower(r.Host)
	return webauthn.New(&webauthn.Config{
		RPID:                  hostname(host),
		RPDisplayName:         rpName,
		RPOrigins:             []string{"https://" + host},
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey: protocol.ResidentKeyRequirementPreferred,
			// Face ID·보안 키의 PIN 같은 «사람 확인» 이 없으면 받지 않는다 — 폰을 든 사람이 본인인지가 목적이다.
			UserVerification: protocol.VerificationRequired,
		},
	})
}

// authView 는 등록·로그인 요청의 답이다. 늘 200 이고 갈래는 Result 다(문구는 login.js 가 정한다).
type authView struct {
	Result   string `json:"result"`
	Ceremony string `json:"ceremony,omitempty"`
	Options  any    `json:"options,omitempty"`
}

// 등록·로그인의 갈래(등록 코드의 갈래는 passkey.go).
const (
	authOK     = "ok"
	authNone   = "none"   // 등록된 패스키가 없다
	authStale  = "stale"  // 시작한 의식이 없거나 시간이 지났다 — 처음부터 다시
	authFailed = "failed" // 서명·origin·사람 확인 등 검증에 실패했다
	authBroken = "broken" // 서버의 패스키 파일을 읽거나 쓰지 못했다
)

type stateView struct {
	Registered int  `json:"registered"` // 등록된 패스키 수(-1: 파일을 읽지 못함)
	Enrolling  bool `json:"enrolling"`  // 쓸 수 있는 등록 코드가 있다
	LoggedIn   bool `json:"loggedIn"`
}

func (s *server) handleAuthState(w http.ResponseWriter, r *http.Request) {
	a := s.auth
	writeJSON(w, http.StatusOK, stateView{
		Registered: a.store.count(),
		Enrolling:  a.enroll.pending(a.now()),
		LoggedIn:   a.loggedIn(r),
	})
}

type authRequest struct {
	Ceremony   string          `json:"ceremony"`
	Code       string          `json:"code"`
	Credential json.RawMessage `json:"credential"`
}

func readAuthRequest(w http.ResponseWriter, r *http.Request) (authRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req authRequest
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "요청을 읽지 못했습니다")
		return authRequest{}, false
	}
	return req, true
}

func (s *server) handleLoginBegin(w http.ResponseWriter, r *http.Request) {
	if _, ok := readAuthRequest(w, r); !ok {
		return
	}
	a, login := s.auth, requestLogin(r)
	rp, err := relyingParty(r)
	if err != nil {
		s.log.Printf("패스키 로그인 %s: %v", login, err)
		writeJSON(w, http.StatusOK, authView{Result: authBroken})
		return
	}
	a.store.mu.Lock()
	f, err := a.store.load()
	a.store.mu.Unlock()
	if err != nil {
		s.log.Printf("패스키 로그인 %s: %v", login, err)
		writeJSON(w, http.StatusOK, authView{Result: authBroken})
		return
	}
	if len(f.Credentials) == 0 {
		writeJSON(w, http.StatusOK, authView{Result: authNone})
		return
	}
	assertion, data, err := rp.BeginLogin(f.user(), webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		s.log.Printf("패스키 로그인 %s: %v", login, err)
		writeJSON(w, http.StatusOK, authView{Result: authBroken})
		return
	}
	id := a.table.begin(ceremony{login: login, data: *data, started: a.now()})
	writeJSON(w, http.StatusOK, authView{Result: authOK, Ceremony: id, Options: assertion})
}

func (s *server) handleLoginFinish(w http.ResponseWriter, r *http.Request) {
	req, ok := readAuthRequest(w, r)
	if !ok {
		return
	}
	a, login := s.auth, requestLogin(r)
	c, ok := a.table.finish(req.Ceremony, false, login, a.now())
	if !ok {
		writeJSON(w, http.StatusOK, authView{Result: authStale})
		return
	}
	result := s.verifyLogin(r, login, c, req.Credential)
	if result == authOK {
		setSessionCookie(w, a.table.open(login, a.now()))
		s.log.Printf("패스키 로그인 %s (%s)", login, deviceHint(r.UserAgent()))
	}
	writeJSON(w, http.StatusOK, authView{Result: result})
}

// verifyLogin 은 서명을 검증하고, 서명 횟수·플래그가 바뀐 자격을 파일에 다시 쓴다(다음 검증이 그 값과 비교한다).
func (s *server) verifyLogin(r *http.Request, login string, c ceremony, raw json.RawMessage) string {
	parsed, err := protocol.ParseCredentialRequestResponseBytes(raw)
	if err != nil {
		s.log.Printf("패스키 로그인 실패 %s: 응답을 읽지 못함: %v", login, err)
		return authFailed
	}
	rp, err := relyingParty(r)
	if err != nil {
		s.log.Printf("패스키 로그인 %s: %v", login, err)
		return authBroken
	}
	st := s.auth.store
	st.mu.Lock()
	defer st.mu.Unlock()
	f, err := st.load()
	if err != nil {
		s.log.Printf("패스키 로그인 %s: %v", login, err)
		return authBroken
	}
	cred, err := rp.ValidateLogin(f.user(), c.data, parsed)
	if err != nil {
		s.log.Printf("패스키 로그인 실패 %s: %v", login, err)
		return authFailed
	}
	// 바뀐 서명 횟수·플래그를 먼저 쓴다 — 복제 의심 표시(CloneWarning)도 함께 남는다. 라이브러리는 그 표시를 지우지
	// 않으므로, 한 번 의심한 자격은 뒤에 더 큰 횟수로 서명해도(복제한 쪽이 앞서 나가도) 늘 거절된다.
	var stored storedCredential
	for i := range f.Credentials {
		if bytes.Equal(f.Credentials[i].Credential.ID, cred.ID) {
			f.Credentials[i].Credential = *cred
			stored = f.Credentials[i]
		}
	}
	if err := st.save(f); err != nil {
		s.log.Printf("패스키 로그인 %s: 자격을 쓰지 못함: %v", login, err)
		return authBroken
	}
	// 서명 횟수가 뒤로 간 적이 있다 — 같은 보안 키가 복제됐을 수 있다. iPhone 패스키는 늘 0이라 걸리지 않는다.
	if cred.Authenticator.CloneWarning {
		s.log.Printf("패스키 로그인 거절 %s: 서명 횟수가 뒤로 갔다(복제 의심) — 이 자격(%s, %s 등록)은 늘 거절한다. "+
			"passkeys.json 에서 그 항목을 지우고 다시 등록한다", login, stored.Device, stored.Added.Format("2006-01-02 15:04"))
		return authFailed
	}
	return authOK
}

func (s *server) handleRegisterBegin(w http.ResponseWriter, r *http.Request) {
	req, ok := readAuthRequest(w, r)
	if !ok {
		return
	}
	a, login := s.auth, requestLogin(r)
	result, codeHash := a.enroll.check(req.Code, a.now())
	if result != codeOK {
		if result == codeWrong || result == codeLocked {
			s.log.Printf("패스키 등록 %s: 코드가 틀렸다 (%s)", login, result)
		}
		writeJSON(w, http.StatusOK, authView{Result: result})
		return
	}
	rp, err := relyingParty(r)
	if err != nil {
		s.log.Printf("패스키 등록 %s: %v", login, err)
		writeJSON(w, http.StatusOK, authView{Result: authBroken})
		return
	}
	a.store.mu.Lock()
	f, err := a.store.load()
	a.store.mu.Unlock()
	if err != nil {
		s.log.Printf("패스키 등록 %s: %v", login, err)
		writeJSON(w, http.StatusOK, authView{Result: authBroken})
		return
	}
	user := f.user()
	if len(user.id) == 0 {
		user.id = []byte(randomToken()) // 처음 등록 — 끝낼 때 파일에 적는다
	}
	creation, data, err := rp.BeginRegistration(user,
		webauthn.WithExclusions(webauthn.Credentials(user.creds).CredentialDescriptors()))
	if err != nil {
		s.log.Printf("패스키 등록 %s: %v", login, err)
		writeJSON(w, http.StatusOK, authView{Result: authBroken})
		return
	}
	id := a.table.begin(ceremony{register: true, login: login, data: *data, codeHash: codeHash, started: a.now()})
	writeJSON(w, http.StatusOK, authView{Result: authOK, Ceremony: id, Options: creation})
}

func (s *server) handleRegisterFinish(w http.ResponseWriter, r *http.Request) {
	req, ok := readAuthRequest(w, r)
	if !ok {
		return
	}
	a, login := s.auth, requestLogin(r)
	c, ok := a.table.finish(req.Ceremony, true, login, a.now())
	if !ok {
		writeJSON(w, http.StatusOK, authView{Result: authStale})
		return
	}
	result := s.register(r, login, c, req.Credential)
	if result == authOK {
		setSessionCookie(w, a.table.open(login, a.now()))
	}
	writeJSON(w, http.StatusOK, authView{Result: result})
}

// register 는 새 자격을 검증하고, 등록 코드를 쓴 뒤에만 파일에 더한다.
func (s *server) register(r *http.Request, login string, c ceremony, raw json.RawMessage) string {
	parsed, err := protocol.ParseCredentialCreationResponseBytes(raw)
	if err != nil {
		s.log.Printf("패스키 등록 실패 %s: 응답을 읽지 못함: %v", login, err)
		return authFailed
	}
	rp, err := relyingParty(r)
	if err != nil {
		s.log.Printf("패스키 등록 %s: %v", login, err)
		return authBroken
	}
	a := s.auth
	st := a.store
	st.mu.Lock()
	defer st.mu.Unlock()
	f, err := st.load()
	if err != nil {
		s.log.Printf("패스키 등록 %s: %v", login, err)
		return authBroken
	}
	// 시작한 뒤 다른 등록이 먼저 끝나 사용자 핸들이 정해졌다 — 이 자격은 그 핸들과 맞지 않는다.
	if len(f.UserID) > 0 && !bytes.Equal(f.UserID, c.data.UserID) {
		return authStale
	}
	user := f.user()
	user.id = c.data.UserID
	cred, err := rp.CreateCredential(user, c.data, parsed)
	if err != nil {
		s.log.Printf("패스키 등록 실패 %s: %v", login, err)
		return authFailed
	}
	if !a.enroll.take(c.codeHash, a.now()) {
		return codeExpired
	}
	f.UserID = c.data.UserID
	f.Credentials = append(f.Credentials, storedCredential{
		Credential: *cred, Added: a.now(), RPID: rp.Config.RPID, Device: deviceHint(r.UserAgent()),
	})
	if err := st.save(f); err != nil {
		s.log.Printf("패스키 등록 %s: 파일을 쓰지 못함: %v", login, err)
		return authBroken
	}
	s.log.Printf("패스키 등록 %s (%s, 모두 %d개)", login, deviceHint(r.UserAgent()), len(f.Credentials))
	return authOK
}

// deviceHint 는 User-Agent 에서 기기 종류만 뽑는다 — 로그와 파일에서 어느 자격인지 알아보라고.
func deviceHint(ua string) string {
	for _, d := range []string{"iPhone", "iPad", "Android", "Macintosh", "Windows", "Linux"} {
		if strings.Contains(ua, d) {
			return d
		}
	}
	return "기기 모름"
}
