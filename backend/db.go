package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"

	_ "modernc.org/sqlite" // Pure Go SQLite driver (uses <3MB RAM)
)

type User struct {
	ID           int64
	Username     string
	PasswordHash string
	IsAdmin      bool
	IsApproved   bool
}

type Message struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content string `json:"content"` // message text
}

// InitDB sets up SQLite with WAL mode and memory caps (<3MB RAM)
func InitDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db: %w", err)
	}

	pragmas := `
	PRAGMA journal_mode = WAL;
	PRAGMA cache_size = -2000;

	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		is_admin BOOLEAN DEFAULT 0,
		is_approved BOOLEAN DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS invite_codes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		code TEXT NOT NULL UNIQUE,
		is_used BOOLEAN DEFAULT 0,
		used_by_username TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		role TEXT NOT NULL,
		content TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_messages_user_id ON messages(user_id, created_at);
	`

	if _, err := db.Exec(pragmas); err != nil {
		return nil, fmt.Errorf("failed to initialize db schema: %w", err)
	}

	return db, nil
}

// CreateUser creates a new user in the database
func CreateUser(db *sql.DB, username, passwordHash string, isAdmin, isApproved bool) (int64, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	res, err := db.Exec(
		"INSERT INTO users (username, password_hash, is_admin, is_approved) VALUES (?, ?, ?, ?)",
		username, passwordHash, isAdmin, isApproved,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetUserByUsername fetches a user record by username
func GetUserByUsername(db *sql.DB, username string) (*User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	u := &User{}
	row := db.QueryRow("SELECT id, username, password_hash, is_admin, is_approved FROM users WHERE username = ?", username)
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.IsAdmin, &u.IsApproved)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// ApproveUser marks a user as approved
func ApproveUser(db *sql.DB, username string) error {
	username = strings.ToLower(strings.TrimSpace(username))
	res, err := db.Exec("UPDATE users SET is_approved = 1 WHERE username = ?", username)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("user '%s' not found", username)
	}
	return nil
}

// GenerateInviteCode creates a new 8-character random invite code
func GenerateInviteCode(db *sql.DB) (string, error) {
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	code := "INV-" + strings.ToUpper(hex.EncodeToString(bytes))

	_, err := db.Exec("INSERT INTO invite_codes (code) VALUES (?)", code)
	if err != nil {
		return "", err
	}
	return code, nil
}

// UseInviteCode validates and marks an invite code as used
func UseInviteCode(db *sql.DB, code, username string) error {
	code = strings.ToUpper(strings.TrimSpace(code))
	var isUsed bool
	err := db.QueryRow("SELECT is_used FROM invite_codes WHERE code = ?", code).Scan(&isUsed)
	if err == sql.ErrNoRows {
		return fmt.Errorf("invalid invite code")
	}
	if err != nil {
		return err
	}
	if isUsed {
		return fmt.Errorf("invite code has already been used")
	}

	_, err = db.Exec("UPDATE invite_codes SET is_used = 1, used_by_username = ? WHERE code = ?", username, username)
	return err
}

// SaveMessage stores a message in history
func SaveMessage(db *sql.DB, userID int64, role, content string) error {
	_, err := db.Exec(
		"INSERT INTO messages (user_id, role, content) VALUES (?, ?, ?)",
		userID, role, content,
	)
	return err
}

// GetUserHistory gets the last N messages for a user in chronological order
func GetUserHistory(db *sql.DB, userID int64, limit int) ([]Message, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	query := `
	SELECT role, content FROM (
		SELECT id, role, content, created_at 
		FROM messages 
		WHERE user_id = ? 
		ORDER BY created_at DESC 
		LIMIT ?
	) ORDER BY id ASC;
	`
	rows, err := db.Query(query, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.Role, &m.Content); err != nil {
			return nil, err
		}
		history = append(history, m)
	}
	return history, nil
}
