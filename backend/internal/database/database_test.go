package database

import (
	"context"
	"testing"
	"time"
)

func TestRefreshTokenRotation(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	user, err := store.UpsertGoogleUser(ctx, "google-123", "person@example.com", "Person", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRefreshToken(ctx, "refresh-1", user.ID, HashToken("secret-1"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	rotated, err := store.RotateRefreshToken(ctx, HashToken("secret-1"), "refresh-2", HashToken("secret-2"), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if rotated.ID != user.ID {
		t.Fatalf("rotated token belongs to %q, want %q", rotated.ID, user.ID)
	}
	if _, err := store.RotateRefreshToken(ctx, HashToken("secret-1"), "refresh-3", HashToken("secret-3"), time.Now().Add(time.Hour)); err != ErrNotFound {
		t.Fatalf("reusing a rotated token returned %v, want ErrNotFound", err)
	}
}
