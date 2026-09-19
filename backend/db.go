package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite" // Pure Go SQLite driver (uses <3MB RAM)
)

type User struct {
	ID           int64
	Username     string
	PasswordHash string
	IsAdmin      bool
	IsApproved   bool
}

type Conversation struct {
	ID        string    `json:"id"`
	UserID    int64     `json:"user_id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Message struct {
	ID             int64     `json:"id,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
	Role           string    `json:"role"`    // "user" or "assistant"
	Content        string    `json:"content"` // message text
	Model          string    `json:"model,omitempty"` // "v1" or "v2"
	CreatedAt      time.Time `json:"created_at,omitempty"`
}

// InitDB sets up SQLite with WAL mode and memory caps (<3MB RAM)
func InitDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db: %w", err)
	}

	pragmas := `
	PRAGMA foreign_keys = ON;
	PRAGMA journal_mode = WAL;
	PRAGMA cache_size = -2000;
	PRAGMA busy_timeout = 5000;

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

	CREATE TABLE IF NOT EXISTS conversations (
		id TEXT PRIMARY KEY,
		user_id INTEGER NOT NULL,
		title TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_conversations_user_id ON conversations(user_id, updated_at DESC);

	CREATE TABLE IF NOT EXISTS messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		conversation_id TEXT,
		user_id INTEGER NOT NULL,
		role TEXT NOT NULL,
		content TEXT NOT NULL,
		model TEXT DEFAULT 'v2',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE,
		FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_messages_user_id ON messages(user_id, created_at);
	`

	if _, err := db.Exec(pragmas); err != nil {
		return nil, fmt.Errorf("failed to initialize db schema: %w", err)
	}

	// Safe migration: Add conversation_id column to messages table if table already existed
	_, _ = db.Exec("ALTER TABLE messages ADD COLUMN conversation_id TEXT;")
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_messages_conv_id ON messages(conversation_id, created_at);")
	_, _ = db.Exec("ALTER TABLE messages ADD COLUMN model TEXT DEFAULT 'v2';")

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

// UseInviteCode validates and marks an invite code as used atomically.
func UseInviteCode(db *sql.DB, code, username string) error {
	code = strings.ToUpper(strings.TrimSpace(code))
	username = strings.ToLower(strings.TrimSpace(username))
	if code == "" {
		return fmt.Errorf("invite code cannot be empty")
	}

	res, err := db.Exec(
		"UPDATE invite_codes SET is_used = 1, used_by_username = ? WHERE code = ? AND is_used = 0",
		username, code,
	)
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}
	if rows == 0 {
		var isUsed bool
		err := db.QueryRow("SELECT is_used FROM invite_codes WHERE code = ?", code).Scan(&isUsed)
		if err == sql.ErrNoRows {
			return fmt.Errorf("invalid invite code")
		}
		if isUsed {
			return fmt.Errorf("invite code has already been used")
		}
		return fmt.Errorf("failed to use invite code")
	}
	return nil
}

// RegisterUserWithInvite creates a user and marks the invite code as used in an atomic transaction
func RegisterUserWithInvite(db *sql.DB, username, passwordHash, inviteCode string) (int64, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	inviteCode = strings.ToUpper(strings.TrimSpace(inviteCode))

	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Atomically burn the invite code
	res, err := tx.Exec(
		"UPDATE invite_codes SET is_used = 1, used_by_username = ? WHERE code = ? AND is_used = 0",
		username, inviteCode,
	)
	if err != nil {
		return 0, fmt.Errorf("database error: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("database error: %w", err)
	}
	if rows == 0 {
		var isUsed bool
		err := tx.QueryRow("SELECT is_used FROM invite_codes WHERE code = ?", inviteCode).Scan(&isUsed)
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("invalid invite code")
		}
		if isUsed {
			return 0, fmt.Errorf("invite code has already been used")
		}
		return 0, fmt.Errorf("failed to use invite code")
	}

	// Create user
	userRes, err := tx.Exec(
		"INSERT INTO users (username, password_hash, is_admin, is_approved) VALUES (?, ?, ?, ?)",
		username, passwordHash, false, true,
	)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return userRes.LastInsertId()
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

// CreateConversation creates a new chat session for the user
func CreateConversation(db *sql.DB, id, title string, userID int64) (*Conversation, error) {
	if id == "" {
		id = "conv_" + strings.ReplaceAll(uuid.New().String(), "-", "")[:16]
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "New Chat"
	}
	now := time.Now().UTC()
	_, err := db.Exec(
		"INSERT INTO conversations (id, user_id, title, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
		id, userID, title, now, now,
	)
	if err != nil {
		return nil, err
	}
	return &Conversation{
		ID:        id,
		UserID:    userID,
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// GetUserConversations fetches all conversations for a user, sorted by most recent first
func GetUserConversations(db *sql.DB, userID int64) ([]Conversation, error) {
	rows, err := db.Query(
		"SELECT id, user_id, title, created_at, updated_at FROM conversations WHERE user_id = ? ORDER BY updated_at DESC",
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var convs []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.UserID, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		convs = append(convs, c)
	}
	return convs, nil
}

// GetConversationByID fetches a single conversation by ID for the user
func GetConversationByID(db *sql.DB, id string, userID int64) (*Conversation, error) {
	var c Conversation
	err := db.QueryRow(
		"SELECT id, user_id, title, created_at, updated_at FROM conversations WHERE id = ? AND user_id = ?",
		id, userID,
	).Scan(&c.ID, &c.UserID, &c.Title, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetConversationMessages gets the messages for a specific conversation in chronological order
func GetConversationMessages(db *sql.DB, conversationID string, userID int64, limit int) ([]Message, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := `
	SELECT id, role, content, COALESCE(model, 'v2'), created_at FROM (
		SELECT id, role, content, model, created_at 
		FROM messages 
		WHERE conversation_id = ? AND user_id = ? 
		ORDER BY created_at DESC 
		LIMIT ?
	) ORDER BY id ASC;
	`
	rows, err := db.Query(query, conversationID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		m.ConversationID = conversationID
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.Model, &m.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, nil
}

// SaveConversationMessage stores a message in a specific conversation and updates its timestamp
func SaveConversationMessage(db *sql.DB, conversationID string, userID int64, role, content string, model ...string) error {
	now := time.Now().UTC()
	msgModel := "v2"
	if len(model) > 0 && model[0] != "" {
		msgModel = model[0]
	}
	_, err := db.Exec(
		"INSERT INTO messages (conversation_id, user_id, role, content, model, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		conversationID, userID, role, content, msgModel, now,
	)
	if err != nil {
		return err
	}

	// Update conversation updated_at timestamp
	_, _ = db.Exec("UPDATE conversations SET updated_at = ? WHERE id = ? AND user_id = ?", now, conversationID, userID)

	// Auto-title the conversation from the first user prompt if it still has default title
	if role == "user" {
		clean := strings.TrimSpace(strings.TrimPrefix(content, "Friend: "))
		if clean != "" {
			runes := []rune(clean)
			if len(runes) > 30 {
				clean = string(runes[:30]) + "…"
			}
			_, _ = db.Exec(
				"UPDATE conversations SET title = ? WHERE id = ? AND user_id = ? AND title = 'New Chat'",
				clean, conversationID, userID,
			)
		}
	}

	return nil
}

// DeleteConversation removes a conversation and all its messages atomically
func DeleteConversation(db *sql.DB, conversationID string, userID int64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec("DELETE FROM messages WHERE conversation_id = ? AND user_id = ?", conversationID, userID)
	if err != nil {
		return err
	}
	res, err := tx.Exec("DELETE FROM conversations WHERE id = ? AND user_id = ?", conversationID, userID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("conversation not found")
	}
	return tx.Commit()
}
