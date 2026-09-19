package main

import (
	"path/filepath"
	"testing"
)

func TestInviteCode_LifecycleAndBurn(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_invite.db")

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	// 1. Generate Invite Code
	code, err := GenerateInviteCode(db)
	if err != nil {
		t.Fatalf("GenerateInviteCode failed: %v", err)
	}
	if code == "" {
		t.Fatal("Expected non-empty invite code")
	}

	// 2. Use Invite Code first time
	username := "bob"
	err = UseInviteCode(db, code, username)
	if err != nil {
		t.Fatalf("First UseInviteCode failed: %v", err)
	}

	// Verify code is properly marked as used in database
	var isUsed bool
	var usedBy string
	err = db.QueryRow("SELECT is_used, used_by_username FROM invite_codes WHERE code = ?", code).Scan(&isUsed, &usedBy)
	if err != nil {
		t.Fatalf("Failed to query invite code status: %v", err)
	}
	if !isUsed {
		t.Fatalf("Expected is_used to be true (1), got %v", isUsed)
	}
	if usedBy != username {
		t.Fatalf("Expected used_by_username to be %q, got %q", username, usedBy)
	}

	// 3. Attempt to reuse the invite code (Must Fail!)
	err = UseInviteCode(db, code, "charlie")
	if err == nil {
		t.Fatal("Expected error when reusing invite code, got nil")
	}

	// 4. Attempt to use an invalid invite code (Must Fail!)
	err = UseInviteCode(db, "INV-INVALID99", "eve")
	if err == nil {
		t.Fatal("Expected error with invalid invite code, got nil")
	}
}

func TestRegisterUserWithInvite_Atomic(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_register.db")

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	code, err := GenerateInviteCode(db)
	if err != nil {
		t.Fatalf("GenerateInviteCode failed: %v", err)
	}

	hash, err := HashPassword("securepassword123")
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	// Register with valid code
	userID, err := RegisterUserWithInvite(db, "alice", hash, code)
	if err != nil {
		t.Fatalf("RegisterUserWithInvite failed: %v", err)
	}
	if userID <= 0 {
		t.Fatalf("Expected valid user ID, got %d", userID)
	}

	// Verify user exists
	user, err := GetUserByUsername(db, "alice")
	if err != nil || user == nil {
		t.Fatalf("Failed to retrieve created user: %v", err)
	}
	if !user.IsApproved {
		t.Fatal("Expected newly registered user with invite to be approved")
	}

	// Second registration with the same code must fail and not create user
	_, err = RegisterUserWithInvite(db, "mallory", hash, code)
	if err == nil {
		t.Fatal("Expected registration with reused invite code to fail")
	}

	mallory, _ := GetUserByUsername(db, "mallory")
	if mallory != nil {
		t.Fatal("User mallory should not have been created")
	}
}

func TestJWT_Lifecycle(t *testing.T) {
	secret := []byte("test-secret-key-32-bytes-long!!")
	user := &User{
		ID:       42,
		Username: "jwtuser",
		IsAdmin:  false,
	}

	token, err := GenerateJWT(user, secret, 1)
	if err != nil {
		t.Fatalf("GenerateJWT failed: %v", err)
	}

	claims, err := ValidateJWT(token, secret)
	if err != nil {
		t.Fatalf("ValidateJWT failed: %v", err)
	}

	if claims.UserID != 42 || claims.Username != "jwtuser" {
		t.Fatalf("Claims mismatch: got ID=%d, Username=%s", claims.UserID, claims.Username)
	}

	// Validate with wrong secret must fail
	_, err = ValidateJWT(token, []byte("wrong-secret-key-32-bytes-long!"))
	if err == nil {
		t.Fatal("Expected validation to fail with wrong secret")
	}
}
