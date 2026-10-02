// Package auth: accounts, passwords (argon2id), sessions and emailed tokens.
// Only hashes of secrets are stored — a database read yields no live session,
// no usable reset link and no password.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"

	mailer "github.com/damonleelcx/play-with-agents/internal/mail"
)

type User struct {
	ID            string     `json:"id"`
	Email         string     `json:"email"`
	Name          string     `json:"name"`
	Role          string     `json:"role"`
	EmailVerified bool       `json:"email_verified"`
	CreatedAt     time.Time  `json:"created_at"`
	Licensed      bool       `json:"licensed"` // holds a VERIFIED licence matching the role
	Admin         bool       `json:"admin"`
	VerifiedAt    *time.Time `json:"-"`
}

type Service struct {
	Pool         *pgxpool.Pool
	Mailer       mailer.Mailer
	PublicOrigin string
	SessionTTL   time.Duration
	AdminEmails  []string
}

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrEmailTaken         = errors.New("an account with this email already exists")
	ErrWeakPassword       = errors.New("password must be at least 10 characters")
	ErrBadEmail           = errors.New("that email address does not look valid")
	ErrTokenInvalid       = errors.New("this link is invalid or has expired")
)

// ── Passwords ──────────────────────────────────────────────────────────────

const (
	argonTime    = 2
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
)

func HashPassword(pw string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

func CheckPassword(hash, pw string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash equalises sign-in timing for unknown emails, so response time does
// not reveal which addresses have accounts.
var dummyHash = HashPassword("timing-equaliser-not-a-real-password")

func ValidatePassword(pw, email string) error {
	n := utf8.RuneCountInString(pw)
	if n < 10 {
		return ErrWeakPassword
	}
	if n > 200 {
		return errors.New("password is too long")
	}
	if strings.EqualFold(pw, email) {
		return errors.New("password must not be your email address")
	}
	return nil
}

func NormalizeEmail(e string) (string, error) {
	e = strings.TrimSpace(e)
	a, err := mail.ParseAddress(e)
	if err != nil || a.Address != e || !strings.Contains(e[strings.LastIndexByte(e, '@')+1:], ".") || len(e) > 254 {
		return "", ErrBadEmail
	}
	return e, nil
}

func token() (raw, hash string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, Hash(raw)
}

func Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ── Accounts ───────────────────────────────────────────────────────────────

func (s *Service) SignUp(ctx context.Context, email, password, name, lang string) (*User, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return nil, err
	}
	if err := ValidatePassword(password, email); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > 80 {
		name = string([]rune(name)[:80])
	}
	var id string
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO users (email, password_hash, name) VALUES ($1,$2,$3) RETURNING id`,
			email, HashPassword(password), name).Scan(&id)
		if err != nil {
			if strings.Contains(err.Error(), "23505") {
				return ErrEmailTaken
			}
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO user_preferences (user_id, data) VALUES ($1, jsonb_build_object('language', $2::text, 'notify_email', true, 'memory_enabled', true))`,
			id, lang)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := s.SendVerification(ctx, id, lang); err != nil {
		// The account exists; the person can ask for another link.
		return s.UserByID(ctx, id)
	}
	return s.UserByID(ctx, id)
}

func (s *Service) SignIn(ctx context.Context, email, password string) (*User, error) {
	var id, hash string
	err := s.Pool.QueryRow(ctx, `SELECT id, password_hash FROM users WHERE lower(email)=lower($1) AND deleted_at IS NULL`,
		strings.TrimSpace(email)).Scan(&id, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		CheckPassword(dummyHash, password)
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if !CheckPassword(hash, password) {
		return nil, ErrInvalidCredentials
	}
	return s.UserByID(ctx, id)
}

func (s *Service) UserByID(ctx context.Context, id string) (*User, error) {
	var u User
	err := s.Pool.QueryRow(ctx, `SELECT u.id, u.email, u.name, u.role, u.email_verified_at, u.created_at,
		EXISTS (SELECT 1 FROM professional_licenses l WHERE l.user_id=u.id AND l.status='verified'
			AND ((u.role='attorney' AND l.kind='bar') OR (u.role='physician' AND l.kind='medical')))
		FROM users u WHERE u.id=$1 AND u.deleted_at IS NULL`, id).
		Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.VerifiedAt, &u.CreatedAt, &u.Licensed)
	if err != nil {
		return nil, err
	}
	u.EmailVerified = u.VerifiedAt != nil
	for _, a := range s.AdminEmails {
		if strings.EqualFold(a, u.Email) && u.EmailVerified {
			u.Admin = true
		}
	}
	if u.Role == "admin" {
		u.Admin = true
	}
	return &u, nil
}

// ── Sessions ───────────────────────────────────────────────────────────────

func (s *Service) CreateSession(ctx context.Context, userID, ua, ip string) (string, time.Time, error) {
	raw, hash := token()
	exp := time.Now().Add(s.SessionTTL)
	_, err := s.Pool.Exec(ctx, `INSERT INTO sessions (id, user_id, expires_at, user_agent, ip) VALUES ($1,$2,$3,$4,$5)`,
		hash, userID, exp, truncate(ua, 300), ip)
	return raw, exp, err
}

// SessionUser resolves a cookie to a user and slides last_seen at most once a
// minute, so every request is not a write.
func (s *Service) SessionUser(ctx context.Context, raw string) (*User, string, error) {
	if raw == "" {
		return nil, "", ErrInvalidCredentials
	}
	hash := Hash(raw)
	var userID string
	err := s.Pool.QueryRow(ctx, `UPDATE sessions SET last_seen_at = CASE WHEN last_seen_at < now()-interval '1 minute' THEN now() ELSE last_seen_at END
		WHERE id=$1 AND revoked_at IS NULL AND expires_at > now() RETURNING user_id`, hash).Scan(&userID)
	if err != nil {
		return nil, "", ErrInvalidCredentials
	}
	u, err := s.UserByID(ctx, userID)
	return u, hash, err
}

func (s *Service) RevokeSession(ctx context.Context, userID, sessionHash string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE sessions SET revoked_at=now() WHERE id=$1 AND user_id=$2`, sessionHash, userID)
	return err
}

func (s *Service) RevokeAllSessions(ctx context.Context, userID, except string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND id<>$2 AND revoked_at IS NULL`, userID, except)
	return err
}

type Session struct {
	ID        string    `json:"id"`
	UserAgent string    `json:"user_agent"`
	IP        string    `json:"ip"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen_at"`
	Current   bool      `json:"current"`
}

func (s *Service) Sessions(ctx context.Context, userID, current string) ([]Session, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, user_agent, ip, created_at, last_seen_at FROM sessions
		WHERE user_id=$1 AND revoked_at IS NULL AND expires_at>now() ORDER BY last_seen_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var x Session
		if err := rows.Scan(&x.ID, &x.UserAgent, &x.IP, &x.CreatedAt, &x.LastSeen); err != nil {
			return nil, err
		}
		x.Current = x.ID == current
		x.ID = x.ID[:16] // enough to address it; never the whole hash
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Service) RevokeSessionPrefix(ctx context.Context, userID, prefix string) error {
	if len(prefix) != 16 {
		return errors.New("bad session id")
	}
	_, err := s.Pool.Exec(ctx, `UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND left(id,16)=$2`, userID, prefix)
	return err
}

// ── Email verification and password reset ─────────────────────────────────

func (s *Service) SendVerification(ctx context.Context, userID, lang string) error {
	u, err := s.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.EmailVerified {
		return nil
	}
	raw, hash := token()
	if _, err := s.Pool.Exec(ctx, `INSERT INTO email_tokens (token_hash, user_id, purpose, expires_at) VALUES ($1,$2,'verify', now()+interval '48 hours')`, hash, userID); err != nil {
		return err
	}
	link := s.PublicOrigin + "/verify-email?token=" + raw
	m := verifyMail(lang, u.Name, link)
	m.To = u.Email
	_, err = s.Mailer.Send(ctx, m)
	return err
}

func (s *Service) VerifyEmail(ctx context.Context, raw string) (*User, error) {
	var userID string
	err := s.Pool.QueryRow(ctx, `UPDATE email_tokens SET used_at=now() WHERE token_hash=$1 AND purpose='verify'
		AND used_at IS NULL AND expires_at>now() RETURNING user_id`, Hash(raw)).Scan(&userID)
	if err != nil {
		return nil, ErrTokenInvalid
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE users SET email_verified_at=coalesce(email_verified_at, now()), updated_at=now() WHERE id=$1`, userID); err != nil {
		return nil, err
	}
	return s.UserByID(ctx, userID)
}

// ForgotPassword always succeeds from the caller's view: whether an account
// exists is not disclosed.
func (s *Service) ForgotPassword(ctx context.Context, email, lang string) error {
	var id, name string
	err := s.Pool.QueryRow(ctx, `SELECT id, name FROM users WHERE lower(email)=lower($1) AND deleted_at IS NULL`, strings.TrimSpace(email)).Scan(&id, &name)
	if err != nil {
		return nil
	}
	// At most 3 live reset links per account per hour.
	var recent int
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM email_tokens WHERE user_id=$1 AND purpose='reset' AND created_at > now()-interval '1 hour'`, id).Scan(&recent)
	if recent >= 3 {
		return nil
	}
	raw, hash := token()
	if _, err := s.Pool.Exec(ctx, `INSERT INTO email_tokens (token_hash, user_id, purpose, expires_at) VALUES ($1,$2,'reset', now()+interval '1 hour')`, hash, id); err != nil {
		return err
	}
	m := resetMail(lang, name, s.PublicOrigin+"/reset-password?token="+raw)
	m.To = strings.TrimSpace(email)
	_, err = s.Mailer.Send(ctx, m)
	return err
}

// ResetPassword consumes the token, sets the password, marks the email verified
// (they proved they receive mail there) and signs out every other session.
func (s *Service) ResetPassword(ctx context.Context, raw, password string) (*User, error) {
	var userID, email string
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `UPDATE email_tokens SET used_at=now() WHERE token_hash=$1 AND purpose='reset'
			AND used_at IS NULL AND expires_at>now() RETURNING user_id`, Hash(raw)).Scan(&userID)
		if err != nil {
			return ErrTokenInvalid
		}
		if err := tx.QueryRow(ctx, `SELECT email FROM users WHERE id=$1`, userID).Scan(&email); err != nil {
			return err
		}
		if err := ValidatePassword(password, email); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2, email_verified_at=coalesce(email_verified_at, now()), updated_at=now() WHERE id=$1`,
			userID, HashPassword(password)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE email_tokens SET used_at=now() WHERE user_id=$1 AND purpose='reset' AND used_at IS NULL`, userID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, userID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.UserByID(ctx, userID)
}

func (s *Service) ChangePassword(ctx context.Context, userID, current, next, keepSession string) error {
	var hash, email string
	if err := s.Pool.QueryRow(ctx, `SELECT password_hash, email FROM users WHERE id=$1`, userID).Scan(&hash, &email); err != nil {
		return err
	}
	if !CheckPassword(hash, current) {
		return ErrInvalidCredentials
	}
	if err := ValidatePassword(next, email); err != nil {
		return err
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE users SET password_hash=$2, updated_at=now() WHERE id=$1`, userID, HashPassword(next)); err != nil {
		return err
	}
	return s.RevokeAllSessions(ctx, userID, keepSession)
}

// DeleteAccount removes the user and, by cascade, everything they own.
func (s *Service) DeleteAccount(ctx context.Context, userID, password string) error {
	var hash string
	if err := s.Pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1`, userID).Scan(&hash); err != nil {
		return err
	}
	if !CheckPassword(hash, password) {
		return ErrInvalidCredentials
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	return err
}

// ── Mail templates (English / 中文) ─────────────────────────────────────────

func verifyMail(lang, name, link string) mailer.Message {
	if lang == "zh" {
		return mailer.Message{Subject: "请验证你的邮箱 · ACT",
			Text: fmt.Sprintf("%s你好，\n\n欢迎来到 ACT。请点击下面的链接验证邮箱（48 小时内有效）：\n\n%s\n\n如果不是你本人注册，请忽略此邮件。\n\n—— 维拉，ACT", greetName(name), link),
			HTML: mailer.Page("验证你的邮箱", greetName(name)+"你好，欢迎来到 ACT。我是维拉，会和持证律师、医生一起照看你的事情。先确认一下这是你的邮箱：", "验证邮箱", link, "链接 48 小时内有效。如果不是你本人注册，请忽略此邮件。")}
	}
	return mailer.Message{Subject: "Confirm your email · ACT",
		Text: fmt.Sprintf("Hi %s,\n\nWelcome to ACT. Confirm your email address with this link (valid for 48 hours):\n\n%s\n\nIf you didn't sign up, you can ignore this message.\n\n— Vera, ACT", orThere(name), link),
		HTML: mailer.Page("Confirm your email", "Hi "+orThere(name)+", welcome to ACT. I'm Vera — I'll look after your matters together with our licensed attorneys and physicians. First, let's confirm this is your email:", "Confirm email", link, "This link is valid for 48 hours. If you didn't sign up, you can ignore this message.")}
}

func resetMail(lang, name, link string) mailer.Message {
	if lang == "zh" {
		return mailer.Message{Subject: "重置你的 ACT 密码",
			Text: fmt.Sprintf("%s你好，\n\n我们收到了重置密码的请求。点击下面的链接设置新密码（1 小时内有效）：\n\n%s\n\n如果不是你本人操作，请忽略此邮件，你的密码不会改变。\n\n—— 维拉，ACT", greetName(name), link),
			HTML: mailer.Page("重置密码", greetName(name)+"你好，我们收到了重置密码的请求。", "设置新密码", link, "链接 1 小时内有效，且只能使用一次。如果不是你本人操作，请忽略此邮件，你的密码不会改变。")}
	}
	return mailer.Message{Subject: "Reset your ACT password",
		Text: fmt.Sprintf("Hi %s,\n\nWe received a request to reset your password. Set a new one with this link (valid for 1 hour):\n\n%s\n\nIf you didn't ask for this, ignore this message — your password won't change.\n\n— Vera, ACT", orThere(name), link),
		HTML: mailer.Page("Reset your password", "Hi "+orThere(name)+", we received a request to reset your password.", "Set a new password", link, "This link is valid for 1 hour and works once. If you didn't ask for this, ignore this message — your password won't change.")}
}

func orThere(n string) string {
	if n == "" {
		return "there"
	}
	return n
}

func greetName(n string) string {
	if n == "" {
		return ""
	}
	return n
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
