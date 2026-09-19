const BASE_URL = process.env.NEXT_PUBLIC_API_URL || "https://aryanssh.duckdns.org"

export type ChatMessage = {
  id?: number
  conversation_id?: string
  role: "user" | "assistant"
  content: string
  created_at: string
}

export type Conversation = {
  id: string
  user_id: number
  title: string
  created_at: string
  updated_at: string
}

type ApiError = { error?: string }

class ApiService {
  private token() {
    return typeof window === "undefined" ? null : localStorage.getItem("auth_token")
  }

  private async request<T>(endpoint: string, options: RequestInit = {}) {
    const headers = new Headers(options.headers)
    headers.set("Content-Type", "application/json")
    const token = this.token()
    if (token) headers.set("Authorization", `Bearer ${token}`)

    const response = await fetch(`${BASE_URL}${endpoint}`, { ...options, headers })
    let data: T & ApiError
    try {
      data = await response.json()
    } catch {
      throw new Error("The server returned an invalid response.")
    }

    if (response.status === 401) {
      localStorage.removeItem("auth_token")
      localStorage.removeItem("auth_user")
      window.dispatchEvent(new Event("auth:unauthorized"))
    }
    if (!response.ok) throw new Error(data.error || "Request failed")
    return data
  }

  async register(payload: { username: string; password: string; invite_code: string }) {
    return this.request<{ message: string }>("/api/auth/register", {
      method: "POST",
      body: JSON.stringify(payload),
    })
  }

  async login(payload: { username: string; password: string }) {
    const data = await this.request<{ token: string; username: string }>("/api/auth/login", {
      method: "POST",
      body: JSON.stringify(payload),
    })
    localStorage.setItem("auth_token", data.token)
    localStorage.setItem("auth_user", data.username)
    return data
  }

  async getConversations() {
    const data = await this.request<{ conversations: Conversation[] }>("/api/conversations")
    return data.conversations || []
  }

  async createConversation(title?: string) {
    return this.request<Conversation>("/api/conversations", {
      method: "POST",
      body: JSON.stringify({ title: title || "New Chat" }),
    })
  }

  async getConversationMessages(id: string) {
    const data = await this.request<{ messages: ChatMessage[] }>(`/api/conversations/${id}`)
    return (data.messages || []).map((message) => ({
      ...message,
      content: message.role === "user" ? message.content.replace(/^Friend:\s*/i, "") : message.content,
    }))
  }

  async deleteConversation(id: string) {
    return this.request<{ message: string }>(`/api/conversations/${id}`, {
      method: "DELETE",
    })
  }

  async getHistory() {
    const data = await this.request<{ history: ChatMessage[] }>("/api/chat/history")
    return (data.history || []).map((message) => ({
      ...message,
      content: message.role === "user" ? message.content.replace(/^Friend:\s*/i, "") : message.content,
    }))
  }

  sendMessage(message: string, conversationId?: string) {
    return this.request<{ reply: string; created_at: string; conversation_id: string }>("/api/chat", {
      method: "POST",
      body: JSON.stringify({ message, conversation_id: conversationId }),
    })
  }

  getGpuStatus() {
    return this.request<{ warm: boolean; seconds_remaining: number }>("/api/chat/status")
  }

  warmup() {
    return this.request<{ status: string; seconds_remaining: number }>("/api/chat/warmup", {
      method: "POST",
    })
  }

  logout() {
    localStorage.removeItem("auth_token")
    localStorage.removeItem("auth_user")
    window.dispatchEvent(new Event("auth:logout"))
  }

  isAuthenticated() {
    return Boolean(this.token())
  }
}

export const api = new ApiService()
