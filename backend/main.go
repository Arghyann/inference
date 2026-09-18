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
	"time"

	"github.com/joho/godotenv"
	modal "github.com/modal-labs/modal-client/go"
	"golang.org/x/crypto/acme/autocert"
)

type Config struct {
	Port      string
	JWTSecret string
	DBPath    string
	Domain    string
	CertDir   string
}

type AppServer struct {
	db            *sql.DB
	modalFunction *modal.Function
	jwtSecret     []byte
}

func main() {
	// 0. Load .env file if present
	_ = godotenv.Load()

	// 1. Parse Command Line Flags (SSH Admin Only)
	createInviteFlag := flag.Bool("create-invite", false, "Generate an invite code from CLI (SSH)")
	createUserFlag := flag.Bool("create-user", false, "Directly create a user from CLI (SSH)")
	usernameFlag := flag.String("u", "", "Username for user creation")
	passwordFlag := flag.String("p", "", "Password for user creation")
	flag.Parse()

	cfg := Config{
		Port:      getEnv("PORT", "8080"),
		JWTSecret: getEnv("JWT_SECRET", "super-secret-default-key-change-in-production"),
		DBPath:    getEnv("DB_PATH", "./inference.db"),
		Domain:    getEnv("DOMAIN", ""),
		CertDir:   getEnv("CERT_DIR", "./certs"),
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
		if *usernameFlag == "" || *passwordFlag == "" {
			log.Fatal("Usage: ./server-bin -create-user -u <username> -p <password>")
		}
		hash, err := HashPassword(*passwordFlag)
		if err != nil {
			log.Fatalf("Error hashing password: %v", err)
		}
		id, err := CreateUser(db, *usernameFlag, hash, false, true)
		if err != nil {
			log.Fatalf("Failed to create user: %v", err)
		}
		fmt.Printf("Successfully created user '%s' (ID: %d)\n", *usernameFlag, id)
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

	method, err := instance.Method("generate")
	if err != nil {
		log.Fatalf("Failed to find generate method: %v", err)
	}
	fmt.Println("Connected to Modal ChatModel successfully!")

	server := &AppServer{
		db:            db,
		modalFunction: method,
		jwtSecret:     []byte(cfg.JWTSecret),
	}

	// 4. Register HTTP Routes (NO ADMIN ROUTES EXPOSED OVER HTTP)
	mux := http.NewServeMux()

	// Public Auth routes (Register strictly requires an invite code generated via SSH)
	mux.HandleFunc("POST /api/auth/register", server.handleRegister)
	mux.HandleFunc("POST /api/auth/login", server.handleLogin)

	// Protected Chat routes (Bearer JWT required)
	mux.HandleFunc("POST /api/chat", server.requireAuth(server.handleChat))
	mux.HandleFunc("GET /api/chat/history", server.requireAuth(server.handleChatHistory))

	// CORS handler to allow any frontend (localhost or production domain) to connect
	corsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		mux.ServeHTTP(w, r)
	})

	if cfg.Domain != "" {
		if err := os.MkdirAll(cfg.CertDir, 0700); err != nil {
			log.Fatalf("Failed to create cert directory: %v", err)
		}

		certManager := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(cfg.Domain),
			Cache:      autocert.DirCache(cfg.CertDir),
		}

		httpsServer := &http.Server{
			Addr:      ":443",
			Handler:   corsHandler,
			TLSConfig: certManager.TLSConfig(),
		}

		// Handle Let's Encrypt HTTP-01 challenge and redirect HTTP to HTTPS
		go func() {
			fmt.Println("HTTP server listening on :80 (ACME challenges & HTTPS redirect)")
			if err := http.ListenAndServe(":80", certManager.HTTPHandler(nil)); err != nil {
				log.Printf("HTTP challenge server stopped: %v", err)
			}
		}()

		fmt.Printf("Inference Backend listening with automatic SSL on https://%s (port 443)\n", cfg.Domain)
		if err := httpsServer.ListenAndServeTLS("", ""); err != nil {
			log.Fatalf("HTTPS Server stopped: %v", err)
		}
	} else {
		fmt.Printf("Inference Backend listening on http://localhost:%s\n", cfg.Port)
		if err := http.ListenAndServe(":"+cfg.Port, corsHandler); err != nil {
			log.Fatalf("Server stopped: %v", err)
		}
	}
}

// ================= HTTP HANDLERS ================= //

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

	req.Username = strings.TrimSpace(req.Username)
	req.InviteCode = strings.TrimSpace(req.InviteCode)

	if req.Username == "" || len(req.Password) < 6 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Username required, password must be at least 6 characters"})
		return
	}

	// 🔒 SECURITY CHECK: Registration strictly requires an invite code generated via SSH
	if req.InviteCode == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "Registration is invite-only. Please provide a valid invite code from the admin.",
		})
		return
	}

	// Validate and burn the invite code
	if err := UseInviteCode(s.db, req.InviteCode, req.Username); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}

	hash, err := HashPassword(req.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to hash password"})
		return
	}

	_, err = CreateUser(s.db, req.Username, hash, false, true)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "Username already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to create user"})
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
	Message string `json:"message"`
}

type ChatResponse struct {
	Reply     string    `json:"reply"`
	CreatedAt time.Time `json:"created_at"`
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

	// 1. Fetch user's previous 25 messages from SQLite for rich context
	history, err := GetUserHistory(s.db, claims.UserID, 25)
	if err != nil {
		log.Printf("Error fetching history: %v", err)
		history = []Message{}
	}

	// 2. Append new user prompt (formatted to match Aryan's training data)
	history = append(history, Message{
		Role:    "user",
		Content: fmt.Sprintf("Friend: %s", prompt),
	})

	// 3. Convert to Modal payload: []any of map[string]any
	payload := make([]any, len(history))
	for i, m := range history {
		payload[i] = map[string]any{
			"role":    m.Role,
			"content": m.Content,
		}
	}

	// 4. Call Modal GPU Function
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	res, err := s.modalFunction.Remote(ctx, []any{payload}, nil)
	if err != nil {
		log.Printf("Modal invocation error: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Model inference failed. Please try again."})
		return
	}

	reply, ok := res.(string)
	if !ok {
		reply = fmt.Sprintf("%v", res)
	}

	// 5. Save user message and AI reply into SQLite
	_ = SaveMessage(s.db, claims.UserID, "user", fmt.Sprintf("Friend: %s", prompt))
	_ = SaveMessage(s.db, claims.UserID, "assistant", reply)

	writeJSON(w, http.StatusOK, ChatResponse{
		Reply:     reply,
		CreatedAt: time.Now().UTC(),
	})
}

func (s *AppServer) handleChatHistory(w http.ResponseWriter, r *http.Request, claims *Claims) {
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
