package auth

import (
	"context"
	"testing"
	"time"

	"github.com/flowverse/flowverse-api/internal/store"
)

func TestPasswordHash(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("correct horse battery staple", hash) {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword("wrong password", hash) {
		t.Fatal("invalid password accepted")
	}
}

func TestRegisterLoginRefreshAndReuse(t *testing.T) {
	repository := store.NewMemory()
	service := New(repository, Config{AccessTTL: time.Minute, RefreshTTL: time.Hour})
	ctx := context.Background()
	user, initial, err := service.Register(ctx, "Person@Example.com", "a secure password", "Test Person")
	if err != nil {
		t.Fatal(err)
	}
	if user.Email != "person@example.com" {
		t.Fatalf("email = %s", user.Email)
	}
	authenticated, err := service.Authenticate(ctx, initial.AccessToken)
	if err != nil || authenticated.ID != user.ID {
		t.Fatalf("authentication failed: %v", err)
	}
	rotated, err := service.Refresh(ctx, initial.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.RefreshToken == initial.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}
	if _, err := service.Refresh(ctx, initial.RefreshToken); err == nil {
		t.Fatal("reused refresh token accepted")
	}
	if _, err := service.Refresh(ctx, rotated.RefreshToken); err == nil {
		t.Fatal("session family was not revoked after reuse")
	}
	if _, _, err := service.Login(ctx, user.Email, "wrong password"); err == nil {
		t.Fatal("invalid password accepted")
	}
}
