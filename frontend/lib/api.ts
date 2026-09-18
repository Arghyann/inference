const BASE_URL = process.env.NEXT_PUBLIC_API_URL || "https://aryanssh.duckdns.org"

export type ChatMessage = {
  id?: number
  role: "user" | "assistant"
  content: string
  created_at: string
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

  async getHistory() {
    const data = await this.request<{ history: ChatMessage[] }>("/api/chat/history")
    return (data.history || []).map((message) => ({
      ...message,
      content: message.role === "user" ? message.content.replace(/^Friend:\s*/i, "") : message.content,
    }))
  }

  sendMessage(message: string) {
    return this.request<{ reply: string; created_at: string }>("/api/chat", {
      method: "POST",
      body: JSON.stringify({ message }),
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
