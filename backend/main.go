package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
	modal "github.com/modal-labs/modal-client/go"
	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/term"
)

type Config struct {
	Port      string
	JWTSecret string
	DBPath    string
	Domain    string
	CertDir   string
}

type AppServer struct {
	db             *sql.DB
	modalGenerate  *modal.Function
	modalWarmup    *modal.Function
	jwtSecret      []byte
	lastActivityMu sync.RWMutex
	lastActivity   time.Time
}

func main() {
	// 0. Load .env file if present
	_ = godotenv.Load()

	// 1. Parse Command Line Flags (SSH Admin Only)
	createInviteFlag := flag.Bool("create-invite", false, "Generate an invite code from CLI (SSH)")
	createUserFlag := flag.Bool("create-user", false, "Directly create a user from CLI (SSH)")
	usernameFlag := flag.String("u", "", "Username for user creation")
	passwordFlag := flag.String("p", "", "Password for user creation (optional; will prompt securely if omitted)")
	flag.Parse()

	cfg := Config{
		Port:      getEnv("PORT", "8080"),
		JWTSecret: getEnv("JWT_SECRET", "super-secret-default-key-change-in-production"),
		DBPath:    getEnv("DB_PATH", "./inference.db"),
		Domain:    getEnv("DOMAIN", ""),
		CertDir:   getEnv("CERT_DIR", "./certs"),
	}

	// Guard against default JWT secret in production
	if cfg.Domain != "" && cfg.JWTSecret == "super-secret-default-key-change-in-production" {
		log.Fatal("FATAL: Insecure default JWT_SECRET cannot be used when DOMAIN is configured for production. Please set JWT_SECRET in your .env file or environment.")
	}

	// 2. Initialize Database
	db, err := InitDB(cfg.DBPath)
	if err != nil {
		log.Fatalf("Database initialization failed: %v", err)
	}
	defer db.Close()

	// SSH ADMIN COMMAND: Generate Invite Code
	if *createInviteFlag {
		code, err := GenerateInviteCode(db)
		if err != nil {
			log.Fatalf("Failed to generate invite code: %v", err)
		}
		fmt.Printf("\nGenerated One-Time Invite Code: %s\nSend this to your friend. They can use it to register.\n\n", code)
		return
	}

	// SSH ADMIN COMMAND: Directly Create a User
	if *createUserFlag {
		if *usernameFlag == "" {
			log.Fatal("Usage: ./server-bin -create-user -u <username> [-p <password>]")
		}
		cleanUser := strings.ToLower(strings.TrimSpace(*usernameFlag))
		if !isValidUsername(cleanUser) {
			log.Fatal("Username must be between 3 and 30 characters (letters, numbers, '-', '_')")
		}

		password := *passwordFlag
		if password == "" {
			fmt.Print("Enter password for user: ")
			bytePassword, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err != nil {
				log.Fatalf("Failed to read password: %v", err)
			}
			password = strings.TrimSpace(string(bytePassword))
		}
		if len(password) < 6 || len(password) > 72 {
			log.Fatal("Password must be between 6 and 72 characters")
		}

		hash, err := HashPassword(password)
		if err != nil {
			log.Fatalf("Error hashing password: %v", err)
		}
		id, err := CreateUser(db, cleanUser, hash, false, true)
		if err != nil {
			log.Fatalf("Failed to create user: %v", err)
		}
		fmt.Printf("Successfully created user '%s' (ID: %d)\n", cleanUser, id)
		return
	}

	// 3. Connect to Modal Cloud GPU
	ctx := context.Background()
	fmt.Println("Connecting to Modal Cloud GPU...")
	client, err := modal.NewClient()
	if err != nil {
		log.Fatalf("Failed to initialize Modal client: %v", err)
	}

	cls, err := client.Cls.FromName(ctx, "aryan-inference", "ChatModel", nil)
	if err != nil {
		log.Fatalf("Failed to lookup ChatModel on Modal: %v", err)
	}

	instance, err := cls.Instance(ctx, nil)
	if err != nil {
		log.Fatalf("Failed to instantiate ChatModel: %v", err)
	}

	generateMethod, err := instance.Method("generate")
	if err != nil {
		log.Fatalf("Failed to find generate method: %v", err)
	}
	fmt.Println("Connected to Modal ChatModel successfully!")

	warmupMethod, err := instance.Method("warmup")
	if err != nil {
		log.Printf("Warmup method not found on Modal (will fallback to generate on first use): %v", err)
	}

	server := &AppServer{
		db:            db,
		modalGenerate: generateMethod,
		modalWarmup:   warmupMethod,
		jwtSecret:     []byte(cfg.JWTSecret),
	}

	// 4. Register HTTP Routes (NO ADMIN ROUTES EXPOSED OVER HTTP)
	mux := http.NewServeMux()

	// In-memory IP Rate Limiters
	loginLimiter := NewIPRateLimiter(5, 1*time.Minute)
	registerLimiter := NewIPRateLimiter(3, 1*time.Minute)
	chatLimiter := NewIPRateLimiter(15, 1*time.Minute)
	warmupLimiter := NewIPRateLimiter(5, 1*time.Minute)

	// Public Auth routes (Protected by IP rate limiting; register requires an invite code generated via SSH)
	mux.HandleFunc("POST /api/auth/register", RateLimitMiddleware(registerLimiter, server.handleRegister))
	mux.HandleFunc("POST /api/auth/login", RateLimitMiddleware(loginLimiter, server.handleLogin))

	// Protected Chat & Conversation routes (Bearer JWT required + Rate Limited)
	mux.HandleFunc("GET /api/conversations", server.requireAuth(server.handleListConversations))
	mux.HandleFunc("POST /api/conversations", server.requireAuth(server.handleCreateConversation))
	mux.HandleFunc("GET /api/conversations/{id}", server.requireAuth(server.handleGetConversation))
	mux.HandleFunc("DELETE /api/conversations/{id}", server.requireAuth(server.handleDeleteConversation))
	mux.HandleFunc("POST /api/chat", RateLimitMiddleware(chatLimiter, server.requireAuth(server.handleChat)))
	mux.HandleFunc("GET /api/chat/history", server.requireAuth(server.handleChatHistory))
	mux.HandleFunc("GET /api/chat/status", server.requireAuth(server.handleGpuStatus))
	mux.HandleFunc("POST /api/chat/warmup", RateLimitMiddleware(warmupLimiter, server.requireAuth(server.handleWarmup)))

	// Security headers & CORS handler
	corsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		mux.ServeHTTP(w, r)
	})

	if cfg.Domain != "" {
		if os.Getenv("TRUST_PROXY") == "" {
			_ = os.Setenv("TRUST_PROXY", "false")
		}

		if err := os.MkdirAll(cfg.CertDir, 0700); err != nil {
			log.Fatalf("Failed to create cert directory: %v", err)
		}

		certManager := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(cfg.Domain),
			Cache:      autocert.DirCache(cfg.CertDir),
		}

		httpsServer := &http.Server{
			Addr:              ":443",
			Handler:           corsHandler,
			TLSConfig:         certManager.TLSConfig(),
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      130 * time.Second,
			IdleTimeout:       120 * time.Second,
		}

		// Handle Let's Encrypt HTTP-01 challenge and redirect HTTP to HTTPS
		go func() {
			fmt.Println("HTTP server listening on :80 (ACME challenges & HTTPS redirect)")
			challengeServer := &http.Server{
				Addr:              ":80",
				Handler:           certManager.HTTPHandler(nil),
				ReadHeaderTimeout: 5 * time.Second,
				ReadTimeout:       10 * time.Second,
				WriteTimeout:      10 * time.Second,
				IdleTimeout:       30 * time.Second,
			}
			if err := challengeServer.ListenAndServe(); err != nil {
				log.Printf("HTTP challenge server stopped: %v", err)
			}
		}()

		fmt.Printf("Inference Backend listening with automatic SSL on https://%s (port 443)\n", cfg.Domain)
		if err := httpsServer.ListenAndServeTLS("", ""); err != nil {
			log.Fatalf("HTTPS Server stopped: %v", err)
		}
	} else {
		fmt.Printf("Inference Backend listening on http://localhost:%s\n", cfg.Port)
		httpServer := &http.Server{
			Addr:              ":" + cfg.Port,
			Handler:           corsHandler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      130 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		if err := httpServer.ListenAndServe(); err != nil {
			log.Fatalf("Server stopped: %v", err)
		}
	}
}

// ================= HTTP HANDLERS ================= //

func isValidUsername(u string) bool {
	if len(u) < 3 || len(u) > 30 {
		return false
	}
	for _, ch := range u {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-') {
			return false
		}
	}
	return true
}

type RegisterRequest struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	InviteCode string `json:"invite_code"` // Mandatory!
}

func (s *AppServer) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	req.Username = strings.ToLower(strings.TrimSpace(req.Username))
	req.InviteCode = strings.ToUpper(strings.TrimSpace(req.InviteCode))

	if !isValidUsername(req.Username) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Username must be 3-30 characters (letters, numbers, '-', '_')",
		})
		return
	}

	if len(req.Password) < 6 || len(req.Password) > 72 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Password must be between 6 and 72 characters",
		})
		return
	}

	// 🔒 SECURITY CHECK: Registration strictly requires an invite code generated via SSH
	if req.InviteCode == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "Registration is invite-only. Please provide a valid invite code from the admin.",
		})
		return
	}

	// Check if username already exists to avoid unnecessary bcrypt hashing
	if existing, _ := GetUserByUsername(s.db, req.Username); existing != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Username already exists"})
		return
	}

	hash, err := HashPassword(req.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to hash password"})
		return
	}

	// Atomically burn the invite code and create user in a single transaction
	_, err = RegisterUserWithInvite(s.db, req.Username, hash, req.InviteCode)
	if err != nil {
		if strings.Contains(err.Error(), "already been used") || strings.Contains(err.Error(), "invalid invite code") {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}
		if strings.Contains(err.Error(), "UNIQUE") {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "Username already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Registration failed"})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"message": "Registration successful! You may now log in.",
	})
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *AppServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	req.Username = strings.ToLower(strings.TrimSpace(req.Username))
	if req.Username == "" || req.Password == "" || len(req.Password) > 72 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid username or password"})
		return
	}

	user, err := GetUserByUsername(s.db, req.Username)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid username or password"})
		return
	}

	if !CheckPasswordHash(req.Password, user.PasswordHash) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid username or password"})
		return
	}

	if !user.IsApproved {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Account not approved."})
		return
	}

	// 24-hour token
	token, err := GenerateJWT(user, s.jwtSecret, 24)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to generate token"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"token":    token,
		"username": user.Username,
	})
}

type ChatRequest struct {
	Message        string `json:"message"`
	ConversationID string `json:"conversation_id,omitempty"`
	Model          string `json:"model,omitempty"`
}

type ChatResponse struct {
	Reply          string    `json:"reply"`
	CreatedAt      time.Time `json:"created_at"`
	ConversationID string    `json:"conversation_id"`
	Model          string    `json:"model"`
}

func (s *AppServer) handleChat(w http.ResponseWriter, r *http.Request, claims *Claims) {
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	prompt := strings.TrimSpace(req.Message)
	if prompt == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Message cannot be empty"})
		return
	}
	if len(prompt) > 4000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Message exceeds maximum limit of 4000 characters"})
		return
	}

	modelChoice := strings.ToLower(strings.TrimSpace(req.Model))
	if modelChoice != "v1" && modelChoice != "v2" {
		modelChoice = "v2"
	}

	conversationID := strings.TrimSpace(req.ConversationID)
	if conversationID == "" {
		conv, err := CreateConversation(s.db, "", "New Chat", claims.UserID)
		if err != nil {
			log.Printf("Error creating conversation: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to create conversation"})
			return
		}
		conversationID = conv.ID
	} else {
		// Verify conversation exists and strictly belongs to this user
		conv, err := GetConversationByID(s.db, conversationID, claims.UserID)
		if err != nil {
			log.Printf("Error verifying conversation: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to verify conversation"})
			return
		}
		if conv == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Conversation not found"})
			return
		}
	}

	// 1. Fetch conversation's previous 25 messages from SQLite for rich context
	convMessages, err := GetConversationMessages(s.db, conversationID, claims.UserID, 25)
	if err != nil {
		log.Printf("Error fetching conversation messages: %v", err)
		convMessages = []Message{}
	}

	// 2. Format history for model prompt
	payload := make([]any, len(convMessages)+1)
	for i, m := range convMessages {
		payload[i] = map[string]any{
			"role":    m.Role,
			"content": m.Content,
		}
	}
	payload[len(convMessages)] = map[string]any{
		"role":    "user",
		"content": fmt.Sprintf("Friend: %s", prompt),
	}

	// 3. Call Modal GPU Function (120s timeout to allow for container cold start + token generation)
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	res, err := s.modalGenerate.Remote(ctx, []any{payload, modelChoice}, nil)
	if err != nil {
		log.Printf("Modal invocation error: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Model inference failed. Please try again."})
		return
	}

	reply, ok := res.(string)
	if !ok {
		reply = fmt.Sprintf("%v", res)
	}

	// Record activity to track warm container status (5-minute idle window)
	s.lastActivityMu.Lock()
	s.lastActivity = time.Now()
	s.lastActivityMu.Unlock()

	// 4. Save user message and AI reply into SQLite under this conversation
	_ = SaveConversationMessage(s.db, conversationID, claims.UserID, "user", fmt.Sprintf("Friend: %s", prompt), modelChoice)
	_ = SaveConversationMessage(s.db, conversationID, claims.UserID, "assistant", reply, modelChoice)

	writeJSON(w, http.StatusOK, ChatResponse{
		Reply:          reply,
		CreatedAt:      time.Now().UTC(),
		ConversationID: conversationID,
		Model:          modelChoice,
	})
}

func (s *AppServer) handleListConversations(w http.ResponseWriter, r *http.Request, claims *Claims) {
	convs, err := GetUserConversations(s.db, claims.UserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to fetch conversations"})
		return
	}
	if convs == nil {
		convs = []Conversation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"conversations": convs,
	})
}

type CreateConversationRequest struct {
	Title string `json:"title"`
}

func (s *AppServer) handleCreateConversation(w http.ResponseWriter, r *http.Request, claims *Claims) {
	var req CreateConversationRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	title := strings.TrimSpace(req.Title)
	if len(title) > 100 {
		runes := []rune(title)
		title = string(runes[:100])
	}
	conv, err := CreateConversation(s.db, "", title, claims.UserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to create conversation"})
		return
	}
	writeJSON(w, http.StatusCreated, conv)
}

func (s *AppServer) handleGetConversation(w http.ResponseWriter, r *http.Request, claims *Claims) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Conversation ID required"})
		return
	}
	conv, err := GetConversationByID(s.db, id, claims.UserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to fetch conversation"})
		return
	}
	if conv == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Conversation not found"})
		return
	}

	messages, err := GetConversationMessages(s.db, id, claims.UserID, 50)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to fetch messages"})
		return
	}
	if messages == nil {
		messages = []Message{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"messages": messages,
	})
}

func (s *AppServer) handleDeleteConversation(w http.ResponseWriter, r *http.Request, claims *Claims) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Conversation ID required"})
		return
	}
	conv, err := GetConversationByID(s.db, id, claims.UserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to find conversation"})
		return
	}
	if conv == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Conversation not found"})
		return
	}

	if err := DeleteConversation(s.db, id, claims.UserID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to delete conversation"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Conversation deleted",
	})
}

const idleWindow = 120 * time.Second // Matches Modal's scaledown_window (2 minutes)

func (s *AppServer) handleGpuStatus(w http.ResponseWriter, r *http.Request, claims *Claims) {
	s.lastActivityMu.RLock()
	last := s.lastActivity
	s.lastActivityMu.RUnlock()

	isWarm := false
	secondsRemaining := 0
	if !last.IsZero() {
		elapsed := time.Since(last)
		if elapsed < idleWindow {
			isWarm = true
			secondsRemaining = int((idleWindow - elapsed).Seconds())
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"warm":              isWarm,
		"seconds_remaining": secondsRemaining,
	})
}

func (s *AppServer) handleWarmup(w http.ResponseWriter, r *http.Request, claims *Claims) {
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	var err error
	if s.modalWarmup != nil {
		_, err = s.modalWarmup.Remote(ctx, []any{}, nil)
	} else {
		// Fallback to generate method if warmup method is not yet deployed on Modal
		pingPayload := []any{
			map[string]any{"role": "user", "content": "ping"},
		}
		_, err = s.modalGenerate.Remote(ctx, []any{pingPayload}, nil)
	}

	if err != nil {
		log.Printf("Warmup failed: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Failed to warm up GPU container. Please try again."})
		return
	}

	s.lastActivityMu.Lock()
	s.lastActivity = time.Now()
	s.lastActivityMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"status":            "ready",
		"seconds_remaining": int(idleWindow.Seconds()),
	})
}

func (s *AppServer) handleChatHistory(w http.ResponseWriter, r *http.Request, claims *Claims) {
	conversationID := r.URL.Query().Get("conversation_id")
	if conversationID != "" {
		messages, err := GetConversationMessages(s.db, conversationID, claims.UserID, 50)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to fetch history"})
			return
		}
		if messages == nil {
			messages = []Message{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"history": messages})
		return
	}

	history, err := GetUserHistory(s.db, claims.UserID, 20)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to fetch history"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"history": history,
	})
}

// ================= MIDDLEWARE ================= //

type authenticatedHandler func(http.ResponseWriter, *http.Request, *Claims)

func (s *AppServer) requireAuth(next authenticatedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Missing or invalid Authorization header"})
			return
		}

		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		claims, err := ValidateJWT(tokenStr, s.jwtSecret)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid or expired token"})
			return
		}

		next(w, r, claims)
	}
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
