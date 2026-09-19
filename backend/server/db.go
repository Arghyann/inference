package main

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type User struct {
	ID           int64
	Username     string
	PasswordHash string
}

type Message struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content string `json:"content"` // Text message
}

// InitDB sets up SQLite with WAL mode and memory caps (<3MB RAM)
func InitDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db: %w", err)
	}

	// Pragmas to keep memory low and prevent write locking
	pragmas := `
	PRAGMA journal_mode = WAL;
	PRAGMA cache_size = -2000;

	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		role TEXT NOT NULL,
		content TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(user_id) REFERENCES users(id)
	);

	CREATE INDEX IF NOT EXISTS idx_messages_user_id ON messages(user_id, created_at);
	`

	if _, err := db.Exec(pragmas); err != nil {
		return nil, fmt.Errorf("failed to initialize db schema: %w", err)
	}

	return db, nil
}

// TODO: Implement CreateUser, GetUserByUsername, SaveMessage, and GetUserHistory below
func CreateUser(db *sql.DB, username, passwordHash string) (int64, error) {

}
