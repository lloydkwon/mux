package serve

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

// 패스키 로그인(docs/design/serve.md «패스키 로그인»). Tailscale 신원 검사 다음의 둘째 겹이다 — 같은 Tailscale
// 계정의 다른 기기나 잠금이 풀린 폰을 든 사람이 Claude 에게 글을 치지 못하게, 이 기기와 사람을 Face ID·보안 키로
// 확인한다. 서버는 공개키만 가진다.

const (
	// enrollCodeDigits 는 등록 코드의 자릿수다. 폰의 숫자 자판으로 친다.
	enrollCodeDigits = 8
	// enrollMaxTries 번 틀리면 그 코드를 지운다 — 새 코드는 데스크톱에서만 나온다.
	enrollMaxTries = 5
	// DefaultEnrollTTL 은 등록 코드가 살아 있는 시간이다.
	DefaultEnrollTTL = 10 * time.Minute
)

// passkeyFile 은 패스키 파일(0600)의 모양이다. UserID 는 처음 등록할 때 정하는 무작위 사용자 핸들이다 —
// 사용자는 한 명뿐이지만 WebAuthn 은 사용자 핸들을 요구한다.
type passkeyFile struct {
	UserID      []byte             `json:"userID"`
	Credentials []storedCredential `json:"credentials"`
}

type storedCredential struct {
	Credential webauthn.Credential `json:"credential"`
	// Added·RPID·Device 는 사람이 파일을 읽고 어느 것을 지울지 고르라고 둔다. 검증에는 쓰지 않는다.
	Added  time.Time `json:"added"`
	RPID   string    `json:"rpID"`
	Device string    `json:"device,omitempty"`
}

// passkeyStore 는 패스키 파일 하나를 읽고 쓴다. 쓰는 것은 이 서버뿐이다(`mux serve enroll` 은 등록 코드 파일만 쓴다).
type passkeyStore struct {
	path string
	mu   sync.Mutex
}

// load 는 파일을 읽는다. 없으면 빈 것이다. 깨진 파일은 오류다 — 비었다고 보고 새로 쓰면 등록한 패스키를 잃는다.
func (p *passkeyStore) load() (passkeyFile, error) {
	data, err := os.ReadFile(p.path)
	if errors.Is(err, fs.ErrNotExist) {
		return passkeyFile{}, nil
	}
	if err != nil {
		return passkeyFile{}, err
	}
	var f passkeyFile
	if err := json.Unmarshal(data, &f); err != nil {
		return passkeyFile{}, fmt.Errorf("%s 를 읽지 못했다: %w", p.path, err)
	}
	return f, nil
}

// save 는 같은 폴더의 임시 파일에 쓰고 이름을 바꾼다 — 쓰다 끊겨도 반쯤 쓴 파일이 남지 않게.
func (p *passkeyStore) save(f passkeyFile) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(p.path, append(data, '\n'))
}

// count 는 등록된 패스키 수다. 읽지 못하면 -1.
func (p *passkeyStore) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	f, err := p.load()
	if err != nil {
		return -1
	}
	return len(f.Credentials)
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // 이름을 바꾼 뒤에는 없는 파일이라 무해하다
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// passkeyUser 는 webauthn.User 다. 사용자는 한 명이고 이름은 화면에만 보인다.
type passkeyUser struct {
	id    []byte
	creds []webauthn.Credential
}

func (u passkeyUser) WebAuthnID() []byte                         { return u.id }
func (u passkeyUser) WebAuthnName() string                       { return "mux" }
func (u passkeyUser) WebAuthnDisplayName() string                { return "Muxtap" }
func (u passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func (f passkeyFile) user() passkeyUser {
	creds := make([]webauthn.Credential, len(f.Credentials))
	for i, c := range f.Credentials {
		creds[i] = c.Credential
	}
	return passkeyUser{id: f.UserID, creds: creds}
}

// ── 등록 코드 ───────────────────────────────────────────────────────

// EnrollPath 는 패스키 파일 곁의 등록 코드 파일 자리다(passkeys.json → passkeys-enroll.json).
// `mux serve enroll` 이 쓰고, 서버가 읽고 지운다.
func EnrollPath(passkeys string) string {
	return strings.TrimSuffix(passkeys, ".json") + "-enroll.json"
}

// enrollFile 은 등록 코드 파일의 모양이다. 코드는 해시로만 둔다.
type enrollFile struct {
	CodeHash string    `json:"codeHash"`
	Expires  time.Time `json:"expires"`
}

// NewEnrollCode 는 새 등록 코드를 만들어 파일에 쓰고, 사람에게 보일 코드를 돌려준다. 앞의 코드는 덮어써서 무효가 된다.
func NewEnrollCode(passkeys string, ttl time.Duration, now time.Time) (string, error) {
	if passkeys == "" {
		return "", errors.New("패스키 파일 자리가 없다")
	}
	limit := new(big.Int).Exp(big.NewInt(10), big.NewInt(enrollCodeDigits), nil)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return "", err
	}
	code := fmt.Sprintf("%0*d", enrollCodeDigits, n)
	data, err := json.Marshal(enrollFile{CodeHash: hashCode(code), Expires: now.Add(ttl)})
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(EnrollPath(passkeys), append(data, '\n')); err != nil {
		return "", err
	}
	return code, nil
}

// FormatEnrollCode 는 코드를 넷씩 끊어 보인다(1234-5678).
func FormatEnrollCode(code string) string {
	if len(code) != enrollCodeDigits {
		return code
	}
	return code[:4] + "-" + code[4:]
}

func hashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// normalizeCode 는 사람이 친 코드에서 숫자만 남긴다(빈칸·하이픈은 버린다).
func normalizeCode(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// 등록 코드 확인의 갈래. 폰의 문구는 login.js 가 정한다.
const (
	codeOK      = "ok"
	codeNone    = "nocode"  // 코드 파일이 없다 — 데스크톱에서 `mux serve enroll` 부터
	codeExpired = "expired" // 시간이 지났다
	codeWrong   = "badcode" // 틀렸다(남은 횟수가 있다)
	codeLocked  = "locked"  // 너무 많이 틀려 코드를 지웠다
)

// enrollGate 는 등록 코드 파일을 읽어 맞혀 본다. 틀린 횟수는 메모리에만 센다 — 코드(해시)가 바뀌면 다시 0부터.
type enrollGate struct {
	path string
	mu   sync.Mutex
	hash string
	miss int
}

// pending 은 지금 쓸 수 있는 코드가 있는가다(등록 화면을 먼저 보일지 정하는 데만 쓴다).
func (g *enrollGate) pending(now time.Time) bool {
	f, err := g.read()
	return err == nil && now.Before(f.Expires)
}

func (g *enrollGate) read() (enrollFile, error) {
	data, err := os.ReadFile(g.path)
	if err != nil {
		return enrollFile{}, err
	}
	var f enrollFile
	if err := json.Unmarshal(data, &f); err != nil || f.CodeHash == "" {
		return enrollFile{}, fmt.Errorf("%s 의 모양이 틀렸다", g.path)
	}
	return f, nil
}

// check 는 친 코드를 맞혀 보고, 맞으면 그 코드의 해시를 돌려준다. 맞혀도 코드를 지우지 않는다 — Face ID 를
// 취소하면 같은 코드로 다시 할 수 있게. 지우는 것은 등록을 끝낼 때의 take 다.
func (g *enrollGate) check(input string, now time.Time) (string, string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	f, err := g.read()
	if err != nil {
		return codeNone, ""
	}
	if !now.Before(f.Expires) {
		return codeExpired, ""
	}
	if f.CodeHash != g.hash {
		g.hash, g.miss = f.CodeHash, 0
	}
	got := hashCode(normalizeCode(input))
	if subtle.ConstantTimeCompare([]byte(got), []byte(f.CodeHash)) == 1 {
		return codeOK, f.CodeHash
	}
	g.miss++
	if g.miss >= enrollMaxTries {
		os.Remove(g.path)
		g.hash, g.miss = "", 0
		return codeLocked, ""
	}
	return codeWrong, ""
}

// take 는 check 가 맞힌 그 코드가 아직 살아 있으면 지우고 true 다. 코드 하나로 패스키를 하나만 등록한다 — 같은
// 코드로 시작한 두 등록 중 뒤에 끝나는 쪽, 그사이 새 코드가 나온 경우, 시간이 지난 경우는 false.
func (g *enrollGate) take(hash string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	f, err := g.read()
	if err != nil || hash == "" || f.CodeHash != hash || !now.Before(f.Expires) {
		return false
	}
	if err := os.Remove(g.path); err != nil {
		return false
	}
	g.hash, g.miss = "", 0
	return true
}
