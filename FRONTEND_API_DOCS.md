# Aryan AI Backend — Frontend Developer Integration Guide

This document contains everything the frontend team needs to build, connect, and deploy a frontend interface against the Aryan AI Inference Backend.

---

## 1. Quick Reference & Base URLs

- **Production API URL (HTTPS):** `https://aryanssh.duckdns.org`
- **Local Development URL (HTTP):** `http://localhost:8080`
- **Content-Type:** `application/json` for all POST requests
- **Auth Header:** `Authorization: Bearer <TOKEN>`
- **CORS:** Enabled (`Access-Control-Allow-Origin: *`). The frontend can run from `localhost:3000`, `localhost:5173`, Vercel, Netlify, or any domain without CORS blocking.

---

## 2. Authentication Flow

The backend uses stateless **JSON Web Tokens (JWT)** with a 24-hour expiration.

```
┌──────────────┐          ┌───────────────────────┐          ┌──────────────┐
│   Register   │ ──(1)──> │  Backend checks code  │ ──(2)──> │ Account Made │
│ (with invite)│          │  and creates user     │          │              │
└──────────────┘          └───────────────────────┘          └──────────────┘
                                                                     │
┌──────────────┐          ┌───────────────────────┐                  │
│  Store JWT   │ <──(4)── │  Backend returns JWT  │ <──(3)───────────┘
│  in storage  │          │  valid for 24 hours   │      User logs in
└──────────────┘          └───────────────────────┘
```

1. **Invite-Only Registration:** Accounts can **only** be created with a valid one-time invite code.
2. **Login:** Returns `{ token, username }`. Store `token` in `localStorage` or a secure cookie.
3. **Subsequent Calls:** Attach `Authorization: Bearer <token>` to all protected endpoints (`/api/chat`, `/api/chat/history`).
4. **Session Expiry (401):** If any protected endpoint returns `401 Unauthorized`, clear the saved token and redirect the user to the Login screen.

---

## 3. TypeScript Interfaces

```typescript
// Auth
export interface RegisterPayload {
  username: string;     // Non-empty, trimmed
  password: string;     // Min 6 characters
  invite_code: string;  // Case-sensitive, e.g. "INV-41CDFBE2"
}

export interface LoginPayload {
  username: string;
  password: string;
}

export interface AuthResponse {
  token: string;
  username: string;
}

// Chat
export interface ChatMessage {
  id?: number;
  user_id?: number;
  role: 'user' | 'assistant';
  content: string;
  created_at: string; // ISO 8601 timestamp
}

export interface SendMessagePayload {
  message: string; // The user's input prompt
}

export interface SendMessageResponse {
  reply: string;      // The model's response
  created_at: string; // ISO timestamp
}

export interface HistoryResponse {
  history: ChatMessage[];
}

// Generic Error Response
export interface ApiError {
  error: string;
}
```

---

## 4. API Endpoints

### 4.1 Register User
- **URL:** `POST /api/auth/register`
- **Auth:** Public
- **Request Body:**
  ```json
  {
    "username": "johndoe",
    "password": "password123",
    "invite_code": "INV-41CDFBE2"
  }
  ```
- **Responses:**
  - `201 Created`:
    ```json
    { "message": "Registration successful! You may now log in." }
    ```
  - `400 Bad Request`: `{"error": "Username required, password must be at least 6 characters"}`
  - `403 Forbidden`: `{"error": "Registration is invite-only. Please provide a valid invite code from the admin."}` or `{"error": "Invalid or expired invite code"}`
  - `409 Conflict`: `{"error": "Username already exists"}`

---

### 4.2 Log In
- **URL:** `POST /api/auth/login`
- **Auth:** Public
- **Request Body:**
  ```json
  {
    "username": "johndoe",
    "password": "password123"
  }
  ```
- **Responses:**
  - `200 OK`:
    ```json
    {
      "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
      "username": "johndoe"
    }
    ```
  - `401 Unauthorized`: `{"error": "Invalid username or password"}`
  - `403 Forbidden`: `{"error": "Account not approved."}`

---

### 4.3 Send Chat Message (Inference)
Invokes the fine-tuned LLaMA model on Modal Cloud GPU. This is an asynchronous AI inference operation; requests typically take **1 to 5 seconds** depending on warm/cold container states.
- **URL:** `POST /api/chat`
- **Auth:** Required (`Authorization: Bearer <TOKEN>`)
- **Request Body:**
  ```json
  {
    "message": "Hey Aryan, tell me about your projects."
  }
  ```
- **Responses:**
  - `200 OK`:
    ```json
    {
      "reply": "Hey! Lately I've been working on fine-tuning LLaMA models on custom conversational data and building inference pipelines.",
      "created_at": "2026-09-18T20:01:30Z"
    }
    ```
  - `400 Bad Request`: `{"error": "Message cannot be empty"}`
  - `401 Unauthorized`: `{"error": "Missing or invalid Authorization header"}` or `{"error": "Invalid or expired token"}`
  - `502 Bad Gateway`: `{"error": "Model inference failed. Please try again."}`

---

### 4.4 Get Chat History
Fetches the user's prior conversation history (up to the last 20 messages).
- **URL:** `GET /api/chat/history`
- **Auth:** Required (`Authorization: Bearer <TOKEN>`)
- **Responses:**
  - `200 OK`:
    ```json
    {
      "history": [
        {
          "id": 1,
          "user_id": 4,
          "role": "user",
          "content": "Friend: Hello Aryan!",
          "created_at": "2026-09-18T19:55:00Z"
        },
        {
          "id": 2,
          "user_id": 4,
          "role": "assistant",
          "content": "Hey! How's it going?",
          "created_at": "2026-09-18T19:55:04Z"
        }
      ]
    }
    ```
  - `401 Unauthorized`: `{"error": "Missing or invalid Authorization header"}`

> **Important UI Note on History:** In the database, user prompts are stored with the fine-tuning prefix `"Friend: "`. When displaying history in the chat UI, strip this prefix:
> ```typescript
> const displayContent = msg.role === 'user' ? msg.content.replace(/^Friend:\s*/i, '') : msg.content;
> ```

---

## 5. Ready-to-Use TypeScript API Client

Save this as `src/lib/api.ts` in your frontend project (React, Next.js, Vue, or Svelte):

```typescript
const BASE_URL = process.env.NEXT_PUBLIC_API_URL || 'https://aryanssh.duckdns.org';

class ApiService {
  private getToken(): string | null {
    if (typeof window === 'undefined') return null;
    return localStorage.getItem('auth_token');
  }

  private async request<T>(endpoint: string, options: RequestInit = {}): Promise<T> {
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      ...(options.headers as Record<string, string>),
    };

    const token = this.getToken();
    if (token) {
      headers['Authorization'] = `Bearer ${token}`;
    }

    const res = await fetch(`${BASE_URL}${endpoint}`, {
      ...options,
      headers,
    });

    if (res.status === 401) {
      if (typeof window !== 'undefined') {
        localStorage.removeItem('auth_token');
        localStorage.removeItem('auth_user');
        window.dispatchEvent(new Event('auth:unauthorized'));
      }
    }

    const data = await res.json();
    if (!res.ok) {
      throw new Error(data.error || 'Request failed');
    }

    return data as T;
  }

  // 1. Register
  async register(payload: { username: string; password: string; invite_code: string }) {
    return this.request<{ message: string }>('/api/auth/register', {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  // 2. Login
  async login(payload: { username: string; password: string }) {
    const data = await this.request<{ token: string; username: string }>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify(payload),
    });
    if (typeof window !== 'undefined') {
      localStorage.setItem('auth_token', data.token);
      localStorage.setItem('auth_user', data.username);
    }
    return data;
  }

  // 3. Send Message
  async sendMessage(message: string) {
    return this.request<{ reply: string; created_at: string }>('/api/chat', {
      method: 'POST',
      body: JSON.stringify({ message }),
    });
  }

  // 4. Fetch History
  async getHistory() {
    const data = await this.request<{ history: Array<{ role: 'user' | 'assistant'; content: string; created_at: string }> }>('/api/chat/history');
    return (data.history || []).map((msg) => ({
      ...msg,
      content: msg.role === 'user' ? msg.content.replace(/^Friend:\s*/i, '') : msg.content,
    }));
  }

  // 5. Logout
  logout() {
    if (typeof window !== 'undefined') {
      localStorage.removeItem('auth_token');
      localStorage.removeItem('auth_user');
      window.dispatchEvent(new Event('auth:logout'));
    }
  }
}

export const api = new ApiService();
```

---

## 6. Frontend UX & Design Recommendations

1. **Markdown Rendering:** AI replies often contain markdown (bold, lists, code snippets). Use `react-markdown` (with `rehype-highlight`) or `marked` to render assistant replies.
2. **Optimistic Message Appending:** When the user hits send:
   - Immediately append the user message to the UI.
   - Show an animated typing bubble / indicator while waiting for `/api/chat`.
   - Replace the typing indicator with the AI response once resolved.
3. **Auto-Scrolling:** Keep the viewport anchored to the latest message as new replies arrive.
4. **Input Handling:** Support `Enter` to submit and `Shift + Enter` for multi-line breaks.
