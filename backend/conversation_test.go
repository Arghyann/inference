package main

import (
	"path/filepath"
	"testing"
)

func TestConversation_DatabaseOperations(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	userID, err := CreateUser(db, "chatuser", "hashedpass", false, true)
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// 1. Create a conversation
	conv, err := CreateConversation(db, "", "New Chat", userID)
	if err != nil {
		t.Fatalf("CreateConversation failed: %v", err)
	}
	if conv.ID == "" {
		t.Fatal("Expected non-empty conversation ID")
	}
	if conv.Title != "New Chat" {
		t.Fatalf("Expected title 'New Chat', got %q", conv.Title)
	}

	// 2. Fetch conversations
	convs, err := GetUserConversations(db, userID)
	if err != nil {
		t.Fatalf("GetUserConversations failed: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("Expected 1 conversation, got %d", len(convs))
	}
	if convs[0].ID != conv.ID {
		t.Fatalf("Expected conv ID %q, got %q", conv.ID, convs[0].ID)
	}

	// 3. Save a message and verify auto-titling from first user prompt
	err = SaveConversationMessage(db, conv.ID, userID, "user", "Friend: What projects are you working on?")
	if err != nil {
		t.Fatalf("SaveConversationMessage failed: %v", err)
	}

	updatedConv, err := GetConversationByID(db, conv.ID, userID)
	if err != nil || updatedConv == nil {
		t.Fatalf("GetConversationByID failed: %v", err)
	}
	if updatedConv.Title != "What projects are you working …" {
		t.Fatalf("Expected auto-title 'What projects are you working …', got %q", updatedConv.Title)
	}

	// 4. Save assistant reply
	err = SaveConversationMessage(db, conv.ID, userID, "assistant", "I am working on LLaMA inference.")
	if err != nil {
		t.Fatalf("Save assistant reply failed: %v", err)
	}

	// 5. Get conversation messages
	msgs, err := GetConversationMessages(db, conv.ID, userID, 10)
	if err != nil {
		t.Fatalf("GetConversationMessages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Fatalf("Unexpected message roles: %v, %v", msgs[0].Role, msgs[1].Role)
	}

	// 6. Delete conversation
	err = DeleteConversation(db, conv.ID, userID)
	if err != nil {
		t.Fatalf("DeleteConversation failed: %v", err)
	}

	convsAfter, err := GetUserConversations(db, userID)
	if err != nil {
		t.Fatalf("GetUserConversations after delete failed: %v", err)
	}
	if len(convsAfter) != 0 {
		t.Fatalf("Expected 0 conversations after delete, got %d", len(convsAfter))
	}

	msgsAfter, err := GetConversationMessages(db, conv.ID, userID, 10)
	if err != nil {
		t.Fatalf("GetConversationMessages after delete failed: %v", err)
	}
	if len(msgsAfter) != 0 {
		t.Fatalf("Expected 0 messages after delete, got %d", len(msgsAfter))
	}
}

func TestNormalizeModel(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"qwen-6k", "qwen-6k"},
		{"qwen-comedy", "qwen-comedy"},
		{"llama-v3", "llama-v3"},
		{"llama-v2", "llama-v2"},
		{"v3", "llama-v3"},
		{"llama-3", "llama-v3"},
		{"v2", "llama-v2"},
		{"v1", "llama-v2"},
		{"", "qwen-6k"},
		{"unknown", "qwen-6k"},
	}

	for _, tt := range tests {
		got := normalizeModel(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeModel(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

func TestConversation_ModelPersistence(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_models.db")

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	userID, err := CreateUser(db, "modeluser", "hashedpass", false, true)
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	conv, err := CreateConversation(db, "", "Model Test", userID)
	if err != nil {
		t.Fatalf("CreateConversation failed: %v", err)
	}

	// Save turn with qwen-6k
	err = SaveConversationMessage(db, conv.ID, userID, "user", "Friend: Hi", "qwen-6k")
	if err != nil {
		t.Fatalf("Failed to save user message: %v", err)
	}
	err = SaveConversationMessage(db, conv.ID, userID, "assistant", "Hello", "qwen-6k")
	if err != nil {
		t.Fatalf("Failed to save assistant message: %v", err)
	}

	// Switch model mid-way to qwen-comedy
	err = SaveConversationMessage(db, conv.ID, userID, "user", "Friend: Make me laugh", "qwen-comedy")
	if err != nil {
		t.Fatalf("Failed to save user message: %v", err)
	}
	err = SaveConversationMessage(db, conv.ID, userID, "assistant", "Why so serious?", "qwen-comedy")
	if err != nil {
		t.Fatalf("Failed to save comedy message: %v", err)
	}

	msgs, err := GetConversationMessages(db, conv.ID, userID, 10)
	if err != nil {
		t.Fatalf("Failed to get messages: %v", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("Expected 4 messages, got %d", len(msgs))
	}
	if msgs[1].Model != "qwen-6k" {
		t.Errorf("Expected first reply model 'qwen-6k', got %q", msgs[1].Model)
	}
	if msgs[3].Model != "qwen-comedy" {
		t.Errorf("Expected second reply model 'qwen-comedy', got %q", msgs[3].Model)
	}
}
