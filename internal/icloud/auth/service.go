package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gpt-go/internal/icloud/domain"
	"gpt-go/internal/icloud/store"
)

const (
	// CookieName 是全局控制台会话 cookie(整个 GPT-GO 控制台共用一道登录门禁)。
	CookieName          = "gptgo_session"
	passwordHashVersion = "pbkdf2_sha256"
	passwordIterations  = 120000
	passwordSaltBytes   = 16
	passwordKeyBytes    = 32
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.@-]{2,63}$`)

type Service struct {
	store      *store.Store
	sessionTTL time.Duration
}

type LoginResult struct {
	Admin     domain.Admin
	Token     string
	ExpiresAt time.Time
}

func NewService(store *store.Store, sessionTTL time.Duration) *Service {
	if sessionTTL <= 0 {
		sessionTTL = 7 * 24 * time.Hour
	}
	return &Service{store: store, sessionTTL: sessionTTL}
}

func (s *Service) Setup(username, password string) (LoginResult, error) {
	username = normalizeUsername(username)
	if err := validateCredentials(username, password); err != nil {
		return LoginResult{}, err
	}
	encoded, err := hashPassword(password)
	if err != nil {
		return LoginResult{}, err
	}
	admin, err := s.store.SetupAdmin(username, encoded)
	if err != nil {
		return LoginResult{}, err
	}
	return s.createSession(admin)
}

func (s *Service) Login(username, password string) (LoginResult, error) {
	admin, ok := s.store.Admin()
	if !ok {
		return LoginResult{}, errors.New("请先完成首次管理员设置")
	}
	if !strings.EqualFold(admin.Username, normalizeUsername(username)) || !verifyPassword(password, admin.PasswordHash) {
		return LoginResult{}, errors.New("账号或密码错误")
	}
	if err := s.store.MarkLogin(); err != nil {
		return LoginResult{}, err
	}
	admin, _ = s.store.Admin()
	return s.createSession(admin)
}

func (s *Service) Authenticate(token string) (domain.Admin, bool) {
	return s.store.ValidateSession(sessionTokenHash(token))
}

func (s *Service) Logout(token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	return s.store.DeleteSession(sessionTokenHash(token))
}

// HasAdmin 报告是否已初始化管理员。
func (s *Service) HasAdmin() bool {
	_, ok := s.store.Admin()
	return ok
}

// ChangePassword 校验旧密码后更新管理员密码(不校验新密码强度,与 seed 一致;
// 登录只走 verifyPassword)。同时使旧会话全部失效(重新登录)。
func (s *Service) ChangePassword(oldPassword, newPassword string) error {
	admin, ok := s.store.Admin()
	if !ok {
		return errors.New("请先完成首次管理员设置")
	}
	if !verifyPassword(oldPassword, admin.PasswordHash) {
		return errors.New("原密码错误")
	}
	if len([]rune(newPassword)) == 0 {
		return errors.New("新密码不能为空")
	}
	if len(newPassword) > 256 {
		return errors.New("密码过长")
	}
	encoded, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	return s.store.UpdateAdminPassword(encoded)
}

func (s *Service) createSession(admin domain.Admin) (LoginResult, error) {
	token, err := randomToken(32)
	if err != nil {
		return LoginResult{}, err
	}
	expiresAt := time.Now().Add(s.sessionTTL)
	if err := s.store.SaveSession(sessionTokenHash(token), expiresAt); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Admin: admin, Token: token, ExpiresAt: expiresAt}, nil
}

func normalizeUsername(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validateCredentials(username, password string) error {
	if !usernamePattern.MatchString(username) {
		return errors.New("账号需要 3-64 位，只能包含字母、数字、点、下划线、短横线或 @")
	}
	if len([]rune(password)) < 8 {
		return errors.New("密码至少 8 位")
	}
	if len(password) > 256 {
		return errors.New("密码过长")
	}
	return nil
}

// HashPassword 导出密码哈希(供模块 bootstrap seed 默认管理员使用,
// 复用与 Setup 完全一致的 pbkdf2_sha256 编码,避免重复实现导致算法漂移)。
func HashPassword(password string) (string, error) {
	return hashPassword(password)
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, passwordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := pbkdf2SHA256([]byte(password), salt, passwordIterations, passwordKeyBytes)
	return fmt.Sprintf("%s$%d$%s$%s",
		passwordHashVersion,
		passwordIterations,
		base64.RawURLEncoding.EncodeToString(salt),
		base64.RawURLEncoding.EncodeToString(key),
	), nil
}

func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != passwordHashVersion {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 10000 || iterations > 1000000 {
		return false
	}
	salt, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(salt) == 0 {
		return false
	}
	want, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false
	}
	got := pbkdf2SHA256([]byte(password), salt, iterations, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func sessionTokenHash(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func pbkdf2SHA256(password, salt []byte, iterations, keyLen int) []byte {
	if iterations <= 0 || keyLen <= 0 {
		return nil
	}
	hashLen := sha256.Size
	blocks := (keyLen + hashLen - 1) / hashLen
	out := make([]byte, 0, blocks*hashLen)
	for block := 1; block <= blocks; block++ {
		mac := hmac.New(sha256.New, password)
		_, _ = mac.Write(salt)
		var counter [4]byte
		binary.BigEndian.PutUint32(counter[:], uint32(block))
		_, _ = mac.Write(counter[:])
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iterations; i++ {
			mac = hmac.New(sha256.New, password)
			_, _ = mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}
