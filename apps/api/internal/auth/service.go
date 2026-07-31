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
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/argon2"

	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/store"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidToken       = errors.New("invalid or expired token")
)

type Config struct {
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

type TokenPair struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	CSRFToken    string    `json:"csrfToken"`
	AccessExpiry time.Time `json:"accessExpiresAt"`
}

type Service struct {
	repository store.Repository
	config     Config
	now        func() time.Time
}

func New(repository store.Repository, config Config) *Service {
	if config.AccessTTL <= 0 {
		config.AccessTTL = 15 * time.Minute
	}
	if config.RefreshTTL <= 0 {
		config.RefreshTTL = 30 * 24 * time.Hour
	}
	return &Service{repository: repository, config: config, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) Register(ctx context.Context, email, password, displayName string) (domain.User, TokenPair, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)
	if _, err := mail.ParseAddress(email); err != nil || !strings.Contains(email, "@") {
		return domain.User{}, TokenPair{}, errors.New("invalid email")
	}
	if len(password) < 12 || len(password) > 128 {
		return domain.User{}, TokenPair{}, errors.New("password must contain between 12 and 128 characters")
	}
	if len([]rune(displayName)) < 1 || len([]rune(displayName)) > 80 {
		return domain.User{}, TokenPair{}, errors.New("displayName must contain between 1 and 80 characters")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return domain.User{}, TokenPair{}, err
	}
	user := domain.User{ID: uuid.NewString(), Email: email, DisplayName: displayName, PasswordHash: hash, CreatedAt: s.now()}
	if err := s.repository.CreateUser(ctx, user); err != nil {
		return domain.User{}, TokenPair{}, err
	}
	pair, err := s.issuePair(ctx, user.ID, uuid.NewString())
	return user, pair, err
}

func (s *Service) Login(ctx context.Context, email, password string) (domain.User, TokenPair, error) {
	user, err := s.repository.UserByEmail(ctx, email)
	if err != nil || !VerifyPassword(password, user.PasswordHash) {
		return domain.User{}, TokenPair{}, ErrInvalidCredentials
	}
	pair, err := s.issuePair(ctx, user.ID, uuid.NewString())
	return user, pair, err
}

func (s *Service) Authenticate(ctx context.Context, raw string) (domain.User, error) {
	session, err := s.validateSession(ctx, raw, "access")
	if err != nil {
		return domain.User{}, err
	}
	return s.repository.UserByID(ctx, session.UserID)
}

func (s *Service) Refresh(ctx context.Context, raw string) (TokenPair, error) {
	hash := HashToken(raw)
	session, err := s.repository.SessionByHash(ctx, hash)
	if err != nil || session.Kind != "refresh" || !session.ExpiresAt.After(s.now()) {
		return TokenPair{}, ErrInvalidToken
	}
	if session.RevokedAt != nil {
		_ = s.repository.RevokeSessionFamily(ctx, session.FamilyID)
		return TokenPair{}, ErrInvalidToken
	}
	if err := s.repository.RevokeSession(ctx, hash); err != nil {
		return TokenPair{}, err
	}
	return s.issuePair(ctx, session.UserID, session.FamilyID)
}

func (s *Service) Logout(ctx context.Context, access, refresh string) {
	if access != "" {
		_ = s.repository.RevokeSession(ctx, HashToken(access))
	}
	if refresh != "" {
		_ = s.repository.RevokeSession(ctx, HashToken(refresh))
	}
}

func (s *Service) validateSession(ctx context.Context, raw, kind string) (domain.Session, error) {
	if raw == "" {
		return domain.Session{}, ErrInvalidToken
	}
	session, err := s.repository.SessionByHash(ctx, HashToken(raw))
	if err != nil || session.Kind != kind || session.RevokedAt != nil || !session.ExpiresAt.After(s.now()) {
		return domain.Session{}, ErrInvalidToken
	}
	return session, nil
}

func (s *Service) issuePair(ctx context.Context, userID, familyID string) (TokenPair, error) {
	access, err := NewToken()
	if err != nil {
		return TokenPair{}, err
	}
	refresh, err := NewToken()
	if err != nil {
		return TokenPair{}, err
	}
	csrf, err := NewToken()
	if err != nil {
		return TokenPair{}, err
	}
	now := s.now()
	accessExpiry := now.Add(s.config.AccessTTL)
	sessions := []domain.Session{
		{TokenHash: HashToken(access), UserID: userID, Kind: "access", ExpiresAt: accessExpiry, FamilyID: familyID},
		{TokenHash: HashToken(refresh), UserID: userID, Kind: "refresh", ExpiresAt: now.Add(s.config.RefreshTTL), FamilyID: familyID},
	}
	for _, session := range sessions {
		if err := s.repository.SaveSession(ctx, session); err != nil {
			return TokenPair{}, err
		}
	}
	return TokenPair{AccessToken: access, RefreshToken: refresh, CSRFToken: csrf, AccessExpiry: accessExpiry}, nil
}

func NewToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func HashPassword(password string) (string, error) {
	const (
		memory      = 64 * 1024
		iterations  = 3
		parallelism = 2
		saltLength  = 16
		keyLength   = 32
	)
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, keyLength)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		memory, iterations, parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	var memory uint64
	var iterations uint64
	var parallelism uint64
	for _, item := range strings.Split(parts[3], ",") {
		keyValue := strings.SplitN(item, "=", 2)
		if len(keyValue) != 2 {
			return false
		}
		value, err := strconv.ParseUint(keyValue[1], 10, 32)
		if err != nil {
			return false
		}
		switch keyValue[0] {
		case "m":
			memory = value
		case "t":
			iterations = value
		case "p":
			parallelism = value
		}
	}
	if memory == 0 || iterations == 0 || parallelism == 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, uint32(iterations), uint32(memory), uint8(parallelism), uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
