package serve

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
)

const (
	testOrigin = "https://" + testHost
	testRPID   = "box.tail1234.ts.net"
	otherLogin = "other@example.com"
)

// passkeyHarness 는 패스키 로그인을 켠 서버와 그 시계다. 파일은 시험마다 임시 폴더에 둔다.
type passkeyHarness struct {
	t     *testing.T
	s     *server
	h     http.Handler
	path  string
	clock time.Time
	logs  *bytes.Buffer
}

func newPasskeyHarness(t *testing.T) *passkeyHarness {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mux", "passkeys.json")
	cfg, err := Config{Addr: "127.0.0.1:7690", Allow: []string{testLogin, otherLogin}, Passkeys: path}.normalized()
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	hz := &passkeyHarness{t: t, path: path, clock: time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC), logs: &logs}
	hz.s = newServer(cfg, newFake(), nil, log.New(&logs, "", 0))
	hz.s.auth.now = func() time.Time { return hz.clock }
	hz.h = hz.s.routes()
	return hz
}

func (hz *passkeyHarness) enroll() string {
	hz.t.Helper()
	code, err := NewEnrollCode(hz.path, DefaultEnrollTTL, hz.clock)
	if err != nil {
		hz.t.Fatal(err)
	}
	return code
}

func (hz *passkeyHarness) post(path string, body any, edit func(*http.Request)) *httptest.ResponseRecorder {
	hz.t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		hz.t.Fatal(err)
	}
	return do(hz.h, request(http.MethodPost, path, string(data), edit))
}

func (hz *passkeyHarness) get(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	return do(hz.h, request(http.MethodGet, path, "", func(r *http.Request) {
		if cookie != nil {
			r.AddCookie(cookie)
		}
	}))
}

type begunView struct {
	Result   string `json:"result"`
	Ceremony string `json:"ceremony"`
	Options  struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			User      struct {
				ID string `json:"id"`
			} `json:"user"`
			AllowCredentials []struct {
				ID string `json:"id"`
			} `json:"allowCredentials"`
			ExcludeCredentials []struct {
				ID string `json:"id"`
			} `json:"excludeCredentials"`
			AuthenticatorSelection struct {
				UserVerification string `json:"userVerification"`
			} `json:"authenticatorSelection"`
			UserVerification string `json:"userVerification"`
		} `json:"publicKey"`
	} `json:"options"`
}

func (hz *passkeyHarness) begin(path string, body any) begunView {
	hz.t.Helper()
	w := hz.post(path, body, nil)
	if w.Code != http.StatusOK {
		hz.t.Fatalf("%s: %d %s", path, w.Code, w.Body)
	}
	return decode[begunView](hz.t, w)
}

// finish 는 끝내기를 보내고 갈래와 (있으면) 로그인 쿠키를 돌려준다.
func (hz *passkeyHarness) finish(path, ceremony string, credential any) (string, *http.Cookie) {
	hz.t.Helper()
	w := hz.post(path, map[string]any{"ceremony": ceremony, "credential": credential}, nil)
	if w.Code != http.StatusOK {
		hz.t.Fatalf("%s: %d %s", path, w.Code, w.Body)
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}
	return decode[authView](hz.t, w).Result, cookie
}

// register 는 코드를 받아 softKey 를 등록하고 로그인 쿠키를 돌려준다.
func (hz *passkeyHarness) register(k *softKey) *http.Cookie {
	hz.t.Helper()
	b := hz.begin("/auth/register/begin", map[string]string{"code": hz.enroll()})
	if b.Result != authOK {
		hz.t.Fatalf("register/begin: %s", b.Result)
	}
	result, cookie := hz.finish("/auth/register/finish", b.Ceremony, k.create(hz.t, b))
	if result != authOK || cookie == nil {
		hz.t.Fatalf("register/finish: %s (쿠키 %v)\n%s", result, cookie, hz.logs)
	}
	return cookie
}

// login 은 softKey 로 로그인한다. flags 로 인증기 플래그를 바꾼다.
func (hz *passkeyHarness) login(k *softKey, flags protocol.AuthenticatorFlags) (string, *http.Cookie) {
	hz.t.Helper()
	b := hz.begin("/auth/login/begin", map[string]string{})
	if b.Result != authOK {
		hz.t.Fatalf("login/begin: %s", b.Result)
	}
	return hz.finish("/auth/login/finish", b.Ceremony, k.get(hz.t, b, flags))
}

// softKey 는 시험용 인증기다 — iPhone 패스키처럼 ES256, 사람 확인(UV), 백업(BE·BS), 서명 횟수 0, attestation 없음.
type softKey struct {
	priv   *ecdsa.PrivateKey
	id     []byte
	user   string
	count  uint32
	origin string
	rpID   string
}

const iPhoneFlags = protocol.FlagUserPresent | protocol.FlagUserVerified | protocol.FlagBackupEligible | protocol.FlagBackupState

func newSoftKey(t *testing.T) *softKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	rand.Read(id)
	return &softKey{priv: priv, id: id, origin: testOrigin, rpID: testRPID}
}

var b64 = base64.RawURLEncoding.EncodeToString

func (k *softKey) authData(flags protocol.AuthenticatorFlags, attested []byte) []byte {
	hash := sha256.Sum256([]byte(k.rpID))
	d := append([]byte{}, hash[:]...)
	d = append(d, byte(flags))
	d = binary.BigEndian.AppendUint32(d, k.count)
	return append(d, attested...)
}

func (k *softKey) clientData(t *testing.T, typ, challenge string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": k.origin, "crossOrigin": false})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func (k *softKey) create(t *testing.T, b begunView) map[string]any {
	t.Helper()
	k.user = b.Options.PublicKey.User.ID
	pub, err := k.priv.PublicKey.ECDH()
	if err != nil {
		t.Fatal(err)
	}
	pt := pub.Bytes() // 0x04 || X || Y
	cose, err := webauthncbor.Marshal(map[int64]any{
		1: int64(webauthncose.EllipticKey), 3: int64(webauthncose.AlgES256), -1: int64(webauthncose.P256),
		-2: pt[1:33], -3: pt[33:65],
	})
	if err != nil {
		t.Fatal(err)
	}
	attested := make([]byte, 16) // AAGUID 0
	attested = binary.BigEndian.AppendUint16(attested, uint16(len(k.id)))
	attested = append(append(attested, k.id...), cose...)
	ad := k.authData(iPhoneFlags|protocol.FlagAttestedCredentialData, attested)
	att, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": ad})
	if err != nil {
		t.Fatal(err)
	}
	cd := k.clientData(t, "webauthn.create", b.Options.PublicKey.Challenge)
	return map[string]any{
		"id": b64(k.id), "rawId": b64(k.id), "type": "public-key",
		"response":               map[string]any{"attestationObject": b64(att), "clientDataJSON": b64(cd), "transports": []string{"internal", "hybrid"}},
		"clientExtensionResults": map[string]any{},
	}
}

func (k *softKey) get(t *testing.T, b begunView, flags protocol.AuthenticatorFlags) map[string]any {
	t.Helper()
	ad := k.authData(flags, nil)
	cd := k.clientData(t, "webauthn.get", b.Options.PublicKey.Challenge)
	cdHash := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), cdHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, k.priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"id": b64(k.id), "rawId": b64(k.id), "type": "public-key",
		"response": map[string]any{
			"authenticatorData": b64(ad), "clientDataJSON": b64(cd), "signature": b64(sig), "userHandle": k.user,
		},
		"clientExtensionResults": map[string]any{},
	}
}

// ── 문지기 ───────────────────────────────────────────────────────────

func TestPasskeyGateLetsOnlyTheLoginPageThrough(t *testing.T) {
	hz := newPasskeyHarness(t)
	if w := hz.get("/", nil); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Fatalf("GET / 로그인 전: %d %q, 로그인 화면으로 보내야 한다", w.Code, w.Header().Get("Location"))
	}
	for _, p := range []string{"/api/list", "/api/features", "/app.js", "/api/screen?session=mux"} {
		if w := hz.get(p, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("GET %s 로그인 전: %d, 401 이어야 한다", p, w.Code)
		}
	}
	if w := hz.post("/api/prompt", map[string]any{"pane": "%12", "pid": 4321, "text": "hi"}, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("POST /api/prompt 로그인 전: %d", w.Code)
	}
	for _, p := range []string{"/login", "/login.js", "/app.css", "/manifest.webmanifest", "/icon-180.png", "/auth/state"} {
		if w := hz.get(p, nil); w.Code != http.StatusOK {
			t.Errorf("GET %s 로그인 전: %d, 열려야 한다", p, w.Code)
		}
	}
	// 공개 경로처럼 보이는 길로 돌아가도 막는다.
	if isPublic("/auth/../api/list") || isPublic("/login/../api/list") {
		t.Error("정리하면 /api/list 인 경로를 공개로 봤다")
	}
	// 로그인 화면도 Tailscale 신원 검사 뒤다.
	w := do(hz.h, request(http.MethodGet, "/login", "", func(r *http.Request) { r.Header.Del("Tailscale-User-Login") }))
	if w.Code != http.StatusForbidden {
		t.Errorf("신원 헤더 없는 /login: %d, 403 이어야 한다", w.Code)
	}
}

func TestPasskeyOffKeepsTheOldBehaviour(t *testing.T) {
	h := testServer(t, newFake())
	if w := do(h, request(http.MethodGet, "/api/list", "", nil)); w.Code != http.StatusOK {
		t.Fatalf("패스키를 끈 /api/list: %d", w.Code)
	}
	if w := do(h, request(http.MethodGet, "/login", "", nil)); w.Code != http.StatusNotFound {
		t.Errorf("패스키를 끈 /login: %d, 없어야 한다", w.Code)
	}
	if w := do(h, request(http.MethodGet, "/manifest.webmanifest", "", nil)); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Muxtap"`) {
		t.Errorf("manifest: %d %s", w.Code, w.Body)
	}
}

// ── 등록과 로그인 ─────────────────────────────────────────────────────

func TestPasskeyRegisterThenLogIn(t *testing.T) {
	hz := newPasskeyHarness(t)
	if st := decode[stateView](t, hz.get("/auth/state", nil)); st.Registered != 0 || st.LoggedIn {
		t.Fatalf("처음 상태: %+v", st)
	}
	k := newSoftKey(t)

	b := hz.begin("/auth/register/begin", map[string]string{"code": FormatEnrollCode(hz.enroll())})
	if b.Result != authOK || b.Options.PublicKey.AuthenticatorSelection.UserVerification != "required" {
		t.Fatalf("register/begin: %+v", b)
	}
	result, cookie := hz.finish("/auth/register/finish", b.Ceremony, k.create(t, b))
	if result != authOK || cookie == nil {
		t.Fatalf("register/finish: %s\n%s", result, hz.logs)
	}
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Errorf("쿠키 속성: %+v", cookie)
	}
	if _, err := os.Stat(EnrollPath(hz.path)); !os.IsNotExist(err) {
		t.Errorf("쓴 등록 코드가 남았다: %v", err)
	}
	if fi, err := os.Stat(hz.path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("패스키 파일: %v %v", fi, err)
	}
	if w := hz.get("/api/list", cookie); w.Code != http.StatusOK {
		t.Fatalf("등록 뒤 /api/list: %d", w.Code)
	}

	// 다른 날, 쿠키 없이: 패스키로 다시 연다.
	lb := hz.begin("/auth/login/begin", map[string]string{})
	if lb.Result != authOK || len(lb.Options.PublicKey.AllowCredentials) != 1 || lb.Options.PublicKey.AllowCredentials[0].ID != b64(k.id) {
		t.Fatalf("login/begin: %+v", lb)
	}
	result, cookie2 := hz.finish("/auth/login/finish", lb.Ceremony, k.get(t, lb, iPhoneFlags))
	if result != authOK || cookie2 == nil || cookie2.Value == cookie.Value {
		t.Fatalf("login/finish: %s (쿠키 %v)\n%s", result, cookie2, hz.logs)
	}
	if w := hz.get("/api/list", cookie2); w.Code != http.StatusOK {
		t.Fatalf("로그인 뒤 /api/list: %d", w.Code)
	}
	if !strings.Contains(hz.logs.String(), "패스키 로그인 "+testLogin) {
		t.Errorf("로그인이 로그에 없다:\n%s", hz.logs)
	}
}

func TestPasskeyLoginRefusesWithoutUserVerification(t *testing.T) {
	hz := newPasskeyHarness(t)
	k := newSoftKey(t)
	hz.register(k)
	// 화면을 누르기만 하고(UP) Face ID 는 없었다(UV 없음).
	result, cookie := hz.login(k, protocol.FlagUserPresent|protocol.FlagBackupEligible|protocol.FlagBackupState)
	if result != authFailed || cookie != nil {
		t.Fatalf("사람 확인 없는 로그인: %s (쿠키 %v), failed 여야 한다", result, cookie)
	}
}

func TestPasskeyLoginRefusesAnotherOriginOrKey(t *testing.T) {
	hz := newPasskeyHarness(t)
	k := newSoftKey(t)
	hz.register(k)

	k.origin = "https://evil.tail9999.ts.net:8443"
	if result, cookie := hz.login(k, iPhoneFlags); result != authFailed || cookie != nil {
		t.Errorf("다른 출처의 응답: %s, failed 여야 한다", result)
	}
	k.origin = testOrigin

	// 같은 자격 id 에 다른 열쇠로 서명했다.
	forged := newSoftKey(t)
	forged.id, forged.user = k.id, k.user
	if result, cookie := hz.login(forged, iPhoneFlags); result != authFailed || cookie != nil {
		t.Errorf("위조한 서명: %s, failed 여야 한다", result)
	}
	if result, _ := hz.login(k, iPhoneFlags); result != authOK {
		t.Errorf("진짜 열쇠는 여전히 열려야 한다: %s\n%s", result, hz.logs)
	}
}

func TestPasskeyLoginRefusesASignCountGoingBack(t *testing.T) {
	hz := newPasskeyHarness(t)
	k := newSoftKey(t) // 보안 키처럼 서명할 때마다 횟수가 는다
	hz.register(k)
	k.count = 5
	if result, _ := hz.login(k, iPhoneFlags); result != authOK {
		t.Fatalf("횟수 5: %s\n%s", result, hz.logs)
	}
	k.count = 3 // 복제한 키가 옛 횟수로 서명했다
	if result, cookie := hz.login(k, iPhoneFlags); result != authFailed || cookie != nil {
		t.Fatalf("횟수가 뒤로 간 로그인: %s, failed 여야 한다", result)
	}
	if !strings.Contains(hz.logs.String(), "복제 의심") {
		t.Errorf("복제 의심이 로그에 없다:\n%s", hz.logs)
	}
	// 복제한 쪽이 더 큰 횟수로 앞서 나가도 한 번 의심한 자격은 계속 닫혀 있다.
	k.count = 10
	if result, cookie := hz.login(k, iPhoneFlags); result != authFailed || cookie != nil {
		t.Fatalf("의심 뒤 더 큰 횟수: %s, 늘 failed 여야 한다", result)
	}
}

func TestPasskeyCeremonyIsUsedOnce(t *testing.T) {
	hz := newPasskeyHarness(t)
	k := newSoftKey(t)
	hz.register(k)
	b := hz.begin("/auth/login/begin", map[string]string{})
	cred := k.get(t, b, iPhoneFlags)
	if result, _ := hz.finish("/auth/login/finish", b.Ceremony, cred); result != authOK {
		t.Fatalf("첫 끝내기: %s", result)
	}
	if result, cookie := hz.finish("/auth/login/finish", b.Ceremony, cred); result != authStale || cookie != nil {
		t.Errorf("같은 응답을 다시 보냈다: %s, stale 이어야 한다", result)
	}
	// 시작한 지 오래된 의식은 버린다.
	b = hz.begin("/auth/login/begin", map[string]string{})
	hz.clock = hz.clock.Add(ceremonyTTL + time.Second)
	if result, _ := hz.finish("/auth/login/finish", b.Ceremony, k.get(t, b, iPhoneFlags)); result != authStale {
		t.Errorf("시간이 지난 의식: %s, stale 이어야 한다", result)
	}
	// 다른 Tailscale 로그인은 남이 시작한 의식을 끝내지 못한다.
	b = hz.begin("/auth/login/begin", map[string]string{})
	w := hz.post("/auth/login/finish", map[string]any{"ceremony": b.Ceremony, "credential": k.get(t, b, iPhoneFlags)},
		func(r *http.Request) { r.Header.Set("Tailscale-User-Login", otherLogin) })
	if got := decode[authView](t, w).Result; got != authStale {
		t.Errorf("남의 의식: %s, stale 이어야 한다", got)
	}
}

// ── 로그인 쿠키 ───────────────────────────────────────────────────────

func TestPasskeySessionEndsAfterThirtyIdleMinutes(t *testing.T) {
	hz := newPasskeyHarness(t)
	cookie := hz.register(newSoftKey(t))
	// 쓰는 동안은 이어진다 — 29분마다 한 번씩 두 번(모두 58분).
	for i := 0; i < 2; i++ {
		hz.clock = hz.clock.Add(29 * time.Minute)
		if w := hz.get("/api/list", cookie); w.Code != http.StatusOK {
			t.Fatalf("%d번째 29분 뒤: %d, 이어져야 한다", i+1, w.Code)
		}
	}
	hz.clock = hz.clock.Add(sessionIdle + time.Second)
	if w := hz.get("/api/list", cookie); w.Code != http.StatusUnauthorized {
		t.Fatalf("30분 넘게 쉰 뒤: %d, 401 이어야 한다", w.Code)
	}
	// 한 번 끝난 쿠키는 다시 살아나지 않는다.
	hz.clock = hz.clock.Add(-sessionIdle)
	if w := hz.get("/api/list", cookie); w.Code != http.StatusUnauthorized {
		t.Errorf("지운 쿠키: %d", w.Code)
	}
}

func TestPasskeySessionBelongsToOneTailscaleLogin(t *testing.T) {
	hz := newPasskeyHarness(t)
	cookie := hz.register(newSoftKey(t))
	w := do(hz.h, request(http.MethodGet, "/api/list", "", func(r *http.Request) {
		r.Header.Set("Tailscale-User-Login", otherLogin)
		r.AddCookie(cookie)
	}))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("다른 로그인이 쓴 쿠키: %d, 401 이어야 한다", w.Code)
	}
	if w := hz.get("/api/list", &http.Cookie{Name: sessionCookie, Value: "made-up"}); w.Code != http.StatusUnauthorized {
		t.Errorf("지어낸 쿠키: %d", w.Code)
	}
}

// ── 등록 코드 ─────────────────────────────────────────────────────────

func TestEnrollCodeWrongTriesDeleteIt(t *testing.T) {
	hz := newPasskeyHarness(t)
	if b := hz.begin("/auth/register/begin", map[string]string{"code": "12345678"}); b.Result != codeNone {
		t.Fatalf("코드 없이: %s", b.Result)
	}
	code := hz.enroll()
	wrong := "00000000"
	if wrong == code {
		wrong = "11111111"
	}
	for i := 1; i < enrollMaxTries; i++ {
		if b := hz.begin("/auth/register/begin", map[string]string{"code": wrong}); b.Result != codeWrong {
			t.Fatalf("%d번째 틀림: %s", i, b.Result)
		}
	}
	if b := hz.begin("/auth/register/begin", map[string]string{"code": wrong}); b.Result != codeLocked {
		t.Fatalf("%d번째 틀림: %s, locked 여야 한다", enrollMaxTries, b.Result)
	}
	if b := hz.begin("/auth/register/begin", map[string]string{"code": code}); b.Result != codeNone {
		t.Errorf("지운 코드를 맞혔다: %s, nocode 여야 한다", b.Result)
	}
	if !strings.Contains(hz.logs.String(), "코드가 틀렸다") {
		t.Errorf("틀린 코드가 로그에 없다:\n%s", hz.logs)
	}
}

func TestEnrollCodeExpiresAndRegistersOnlyOneKey(t *testing.T) {
	hz := newPasskeyHarness(t)
	code := hz.enroll()
	hz.clock = hz.clock.Add(DefaultEnrollTTL)
	if b := hz.begin("/auth/register/begin", map[string]string{"code": code}); b.Result != codeExpired {
		t.Fatalf("시간이 지난 코드: %s", b.Result)
	}
	k1, k2, k3, k4 := newSoftKey(t), newSoftKey(t), newSoftKey(t), newSoftKey(t)
	register := func(b begunView, k *softKey) string {
		r, cookie := hz.finish("/auth/register/finish", b.Ceremony, k.create(t, b))
		if (r == authOK) != (cookie != nil) {
			t.Fatalf("갈래 %s 와 쿠키 %v 가 어긋난다", r, cookie)
		}
		return r
	}
	count := func(want int) {
		t.Helper()
		if n := hz.s.auth.store.count(); n != want {
			t.Fatalf("등록된 패스키 %d개, %d개여야 한다", n, want)
		}
	}

	// 첫 등록을 두 기기가 같은 코드로 겨뤘다 — 빈 파일이라 둘 다 사용자 핸들을 새로 정했고, 뒤에 끝난 쪽은 핸들이 달라 stale.
	code = hz.enroll()
	b1 := hz.begin("/auth/register/begin", map[string]string{"code": code})
	b2 := hz.begin("/auth/register/begin", map[string]string{"code": code})
	if r := register(b1, k1); r != authOK {
		t.Fatalf("첫 등록: %s\n%s", r, hz.logs)
	}
	if r := register(b2, k2); r != authStale {
		t.Fatalf("같은 코드의 둘째 첫 등록: %s, stale 이어야 한다", r)
	}
	count(1)

	// 패스키가 있을 때 같은 코드로 둘이 시작했다 — 핸들은 같으니 막는 것은 «코드 하나에 패스키 하나»(take) 다.
	code = hz.enroll()
	b3 := hz.begin("/auth/register/begin", map[string]string{"code": code})
	b4 := hz.begin("/auth/register/begin", map[string]string{"code": code})
	if len(b3.Options.PublicKey.ExcludeCredentials) != 1 || b3.Options.PublicKey.ExcludeCredentials[0].ID != b64(k1.id) {
		t.Errorf("제외 목록: %+v", b3.Options.PublicKey.ExcludeCredentials)
	}
	if r := register(b3, k2); r != authOK {
		t.Fatalf("둘째 기기: %s\n%s", r, hz.logs)
	}
	if r := register(b4, k3); r != codeExpired {
		t.Fatalf("같은 코드의 셋째 등록: %s, expired 여야 한다", r)
	}
	count(2)

	// 옛 코드로 시작한 뒤 데스크톱이 새 코드를 냈다 — 옛 코드의 등록이 새 코드를 써 버리지 않는다.
	old := hz.begin("/auth/register/begin", map[string]string{"code": hz.enroll()})
	fresh := hz.enroll()
	if r := register(old, k3); r != codeExpired {
		t.Fatalf("새 코드가 나온 뒤 옛 코드의 등록: %s, expired 여야 한다", r)
	}
	if b := hz.begin("/auth/register/begin", map[string]string{"code": fresh}); b.Result != authOK {
		t.Fatalf("새 코드가 살아 있어야 한다: %s", b.Result)
	}
	// 코드가 끝나기 1분 전에 시작해 2분 뒤에 끝냈다 — 의식(3분)은 살아 있어도 코드는 지났다.
	late := hz.enroll()
	hz.clock = hz.clock.Add(DefaultEnrollTTL - time.Minute)
	b5 := hz.begin("/auth/register/begin", map[string]string{"code": late})
	if b5.Result != authOK {
		t.Fatalf("끝나기 1분 전의 코드: %s", b5.Result)
	}
	hz.clock = hz.clock.Add(2 * time.Minute)
	if r := register(b5, k3); r != codeExpired {
		t.Fatalf("코드 시간이 지난 뒤 끝낸 등록: %s, expired 여야 한다", r)
	}
	count(2)

	// 새 코드로는 된다.
	if r := register(hz.begin("/auth/register/begin", map[string]string{"code": hz.enroll()}), k4); r != authOK {
		t.Fatalf("새 코드로 등록: %s\n%s", r, hz.logs)
	}
	for _, k := range []*softKey{k1, k2, k4} {
		if r, _ := hz.login(k, iPhoneFlags); r != authOK {
			t.Errorf("등록한 열쇠는 모두 열려야 한다: %s\n%s", r, hz.logs)
		}
	}
}

func TestEnrollRefusesBrokenPasskeyFile(t *testing.T) {
	hz := newPasskeyHarness(t)
	if err := os.MkdirAll(filepath.Dir(hz.path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hz.path, []byte("{깨짐"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b := hz.begin("/auth/register/begin", map[string]string{"code": hz.enroll()}); b.Result != authBroken {
		t.Fatalf("깨진 파일에 등록: %s, broken 이어야 한다", b.Result)
	}
	if b := hz.begin("/auth/login/begin", map[string]string{}); b.Result != authBroken {
		t.Fatalf("깨진 파일로 로그인: %s, broken 이어야 한다", b.Result)
	}
	if data, _ := os.ReadFile(hz.path); string(data) != "{깨짐" {
		t.Errorf("깨진 파일을 덮어썼다: %q", data)
	}
	if st := decode[stateView](t, hz.get("/auth/state", nil)); st.Registered != -1 {
		t.Errorf("깨진 파일의 상태: %+v", st)
	}
}

func TestNormalizeAndFormatEnrollCode(t *testing.T) {
	if got := normalizeCode(" 1234-5678 "); got != "12345678" {
		t.Errorf("normalizeCode: %q", got)
	}
	if got := FormatEnrollCode("12345678"); got != "1234-5678" {
		t.Errorf("FormatEnrollCode: %q", got)
	}
	if got := EnrollPath("/x/passkeys.json"); got != "/x/passkeys-enroll.json" {
		t.Errorf("EnrollPath: %q", got)
	}
}
