"use client"

import { FormEvent, KeyboardEvent, useEffect, useRef, useState } from "react"
import { ArrowUp, LogOut, MessageCircle, Sparkles, UserRound, Zap } from "lucide-react"
import { api, ChatMessage } from "@/lib/api"

type AuthMode = "login" | "register"

export default function ChatbotUI() {
  const [authenticated, setAuthenticated] = useState(false)
  const [username, setUsername] = useState("")
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState("")
  const [isLoading, setIsLoading] = useState(false)
  const [loadingElapsed, setLoadingElapsed] = useState(0)
  const [isHistoryLoading, setIsHistoryLoading] = useState(false)
  const [authMode, setAuthMode] = useState<AuthMode>("login")
  const [authError, setAuthError] = useState("")
  const [authNotice, setAuthNotice] = useState("")
  const [authLoading, setAuthLoading] = useState(false)
  const [gpuWarm, setGpuWarm] = useState<boolean | null>(null)
  const [isWarmingUp, setIsWarmingUp] = useState(false)
  const messagesEndRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const handleSignedOut = () => {
      setAuthenticated(false)
      setMessages([])
      setUsername("")
    }
    if (api.isAuthenticated()) {
      setAuthenticated(true)
      setUsername(localStorage.getItem("auth_user") || "")
    }
    window.addEventListener("auth:unauthorized", handleSignedOut)
    window.addEventListener("auth:logout", handleSignedOut)
    return () => {
      window.removeEventListener("auth:unauthorized", handleSignedOut)
      window.removeEventListener("auth:logout", handleSignedOut)
    }
  }, [])

  useEffect(() => {
    if (!authenticated) return
    setIsHistoryLoading(true)
    api.getHistory()
      .then(setMessages)
      .catch(() => setAuthError("Could not load your conversation history."))
      .finally(() => setIsHistoryLoading(false))

    // Check GPU status on load
    api.getGpuStatus()
      .then((res) => setGpuWarm(res.warm))
      .catch(() => {})

    // Check GPU status periodically every 25 seconds
    const interval = setInterval(() => {
      api.getGpuStatus()
        .then((res) => setGpuWarm(res.warm))
        .catch(() => {})
    }, 25000)

    return () => clearInterval(interval)
  }, [authenticated])

  useEffect(() => {
    if (!isLoading) {
      setLoadingElapsed(0)
      return
    }
    const timer = setInterval(() => {
      setLoadingElapsed((prev) => prev + 1)
    }, 1000)
    return () => clearInterval(timer)
  }, [isLoading])

  async function handleWarmupGpu() {
    if (isWarmingUp) return
    setIsWarmingUp(true)
    try {
      await api.warmup()
      setGpuWarm(true)
    } catch (error) {
      console.error("GPU warmup failed:", error)
    } finally {
      setIsWarmingUp(false)
    }
  }

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" })
  }, [messages, isLoading])

  async function handleAuth(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setAuthError("")
    setAuthNotice("")
    const form = new FormData(event.currentTarget)
    const name = String(form.get("username") || "").trim()
    const password = String(form.get("password") || "")
    const inviteCode = String(form.get("invite_code") || "").trim()
    if (!name || password.length < 6 || (authMode === "register" && !inviteCode)) {
      setAuthError(authMode === "register" ? "Enter a username, 6+ character password, and invite code." : "Enter your username and a password with at least 6 characters.")
      return
    }
    setAuthLoading(true)
    try {
      if (authMode === "register") {
        await api.register({ username: name, password, invite_code: inviteCode })
        setAuthMode("login")
        setAuthNotice("Account created. You can log in now.")
      } else {
        const result = await api.login({ username: name, password })
        setUsername(result.username)
        setAuthenticated(true)
      }
    } catch (error) {
      setAuthError(error instanceof Error ? error.message : "Authentication failed")
    } finally {
      setAuthLoading(false)
    }
  }

  async function sendMessage(event?: FormEvent) {
    event?.preventDefault()
    const content = input.trim()
    if (!content || isLoading) return
    setInput("")
    setMessages((current) => [...current, { role: "user", content, created_at: new Date().toISOString() }])
    setIsLoading(true)
    try {
      const response = await api.sendMessage(content)
      setMessages((current) => [...current, { role: "assistant", content: response.reply, created_at: response.created_at }])
      setGpuWarm(true)
    } catch (error) {
      setMessages((current) => [...current, { role: "assistant", content: error instanceof Error ? error.message : "Something went wrong. Please try again.", created_at: new Date().toISOString() }])
    } finally {
      setIsLoading(false)
    }
  }

  function handleKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing && event.keyCode !== 229) {
      event.preventDefault()
      void sendMessage()
    }
  }

  if (!authenticated) {
    return (
      <main className="min-h-screen bg-[#090b0a] px-5 py-10 text-white">
        <div className="mx-auto flex min-h-[calc(100vh-5rem)] max-w-md items-center justify-center">
          <section className="w-full rounded-3xl border border-white/10 bg-white/[0.045] p-8 shadow-2xl shadow-black/30 backdrop-blur-xl">
            <h1 className="text-3xl font-semibold tracking-tight">{authMode === "login" ? "Welcome back" : "Create your account"}</h1>
            <p className="mt-2 text-sm text-white/50">{authMode === "login" ? "Sign in to continue your conversation." : "Access is invite-only for this workspace."}</p>
            <form onSubmit={handleAuth} className="mt-7 space-y-4">
              <input name="username" autoComplete="username" placeholder="Username" className="h-12 w-full rounded-xl border border-white/10 bg-black/20 px-4 text-sm outline-none transition placeholder:text-white/30 focus:border-lime-300/60" />
              <input name="password" type="password" autoComplete={authMode === "login" ? "current-password" : "new-password"} placeholder="Password" className="h-12 w-full rounded-xl border border-white/10 bg-black/20 px-4 text-sm outline-none transition placeholder:text-white/30 focus:border-lime-300/60" />
              {authMode === "register" && <input name="invite_code" placeholder="Invite code" className="h-12 w-full rounded-xl border border-white/10 bg-black/20 px-4 text-sm outline-none transition placeholder:text-white/30 focus:border-lime-300/60" />}
              {authError && <p className="rounded-lg border border-red-400/20 bg-red-400/10 px-3 py-2 text-sm text-red-200">{authError}</p>}
              {authNotice && <p className="rounded-lg border border-lime-300/20 bg-lime-300/10 px-3 py-2 text-sm text-lime-200">{authNotice}</p>}
              <button disabled={authLoading} className="h-12 w-full rounded-xl bg-lime-400 font-semibold text-black transition hover:bg-lime-300 disabled:cursor-wait disabled:opacity-60">{authLoading ? "Please wait…" : authMode === "login" ? "Sign in" : "Create account"}</button>
            </form>
            <button onClick={() => { setAuthMode(authMode === "login" ? "register" : "login"); setAuthError(""); setAuthNotice("") }} className="mt-5 w-full text-center text-sm text-white/50 hover:text-white">{authMode === "login" ? "Need an account? Request access" : "Already have an account? Sign in"}</button>
          </section>
        </div>
      </main>
    )
  }

  return (
    <main className="min-h-screen bg-[#090b0a] px-4 py-4 text-white sm:px-6 sm:py-6">
      <div className="mx-auto flex min-h-[calc(100vh-2rem)] max-w-5xl flex-col overflow-hidden rounded-[28px] border border-white/10 bg-white/[0.035] shadow-2xl shadow-black/40 backdrop-blur-xl sm:min-h-[calc(100vh-3rem)]">
        <header className="flex items-center justify-between border-b border-white/10 px-5 py-4 sm:px-7">
          <div className="flex items-center gap-3">
            <div className="grid size-9 place-items-center rounded-xl bg-lime-400 text-black">
              <MessageCircle size={18} />
            </div>
            <div>
              <h1 className="font-semibold tracking-tight">Aryan AI</h1>
              <p className="text-xs text-white/40">Fine-tuned assistant · online</p>
            </div>
          </div>

          <div className="flex items-center gap-2.5 sm:gap-3">
            {isWarmingUp ? (
              <div className="flex items-center gap-2 rounded-full border border-amber-400/30 bg-amber-400/10 px-3 py-1.5 text-xs text-amber-300">
                <span className="size-2 animate-ping rounded-full bg-amber-400" />
                <span>Waking GPU (~25s)...</span>
              </div>
            ) : gpuWarm ? (
              <div
                className="flex items-center gap-1.5 rounded-full border border-lime-400/30 bg-lime-400/10 px-3 py-1.5 text-xs font-medium text-lime-300"
                title="GPU container is warm and ready for fast responses"
              >
                <span className="size-2 rounded-full bg-lime-400" />
                <span className="hidden sm:inline">GPU</span> Ready
              </div>
            ) : (
              <button
                type="button"
                onClick={handleWarmupGpu}
                disabled={isWarmingUp}
                className="flex items-center gap-1.5 rounded-full border border-amber-400/30 bg-amber-400/10 px-3 py-1.5 text-xs font-medium text-amber-300 transition hover:bg-amber-400/20 active:scale-95"
                title="Container is sleeping. Click to wake up GPU beforehand."
              >
                <Zap size={13} className="text-amber-400" />
                <span>Wake GPU</span>
              </button>
            )}

            <button
              onClick={() => api.logout()}
              className="flex items-center gap-2 rounded-lg px-3 py-2 text-sm text-white/50 transition hover:bg-white/10 hover:text-white"
            >
              <UserRound size={15} />
              {username}
              <LogOut size={15} />
            </button>
          </div>
        </header>

        <section className="flex flex-1 flex-col overflow-hidden">
          <div className="flex-1 overflow-y-auto px-5 py-7 sm:px-20 sm:py-10">
            {isHistoryLoading ? (
              <div className="flex items-center justify-center py-20 text-sm text-white/40">Loading your history…</div>
            ) : messages.length === 0 ? (
              <div className="mx-auto flex max-w-lg flex-col items-center justify-center py-20 text-center">
                <div className="mb-5 grid size-14 place-items-center rounded-2xl border border-lime-300/20 bg-lime-300/10 text-lime-300">
                  <Sparkles />
                </div>
                <h2 className="text-2xl font-semibold">What can I help with?</h2>
                <p className="mt-2 text-sm text-white/45">Ask Aryan AI anything about projects, ideas, or technical work.</p>
              </div>
            ) : (
              <div className="mx-auto max-w-2xl space-y-6">
                {messages.map((message, index) => (
                  <div key={`${message.created_at}-${index}`} className={`flex gap-3 ${message.role === "user" ? "justify-end" : "justify-start"}`}>
                    <div className={`max-w-[85%] rounded-2xl px-4 py-3 text-sm leading-6 ${message.role === "user" ? "bg-lime-400 text-black" : "border border-white/10 bg-white/[0.06] text-white/85"}`}>
                      <p className="whitespace-pre-wrap">{message.content}</p>
                    </div>
                  </div>
                ))}
                {isLoading && (
                  <div className="flex items-center gap-2 text-sm text-white/45">
                    <span className="size-2 animate-pulse rounded-full bg-lime-300" />
                    <span className="size-2 animate-pulse rounded-full bg-lime-300 [animation-delay:150ms]" />
                    <span className="size-2 animate-pulse rounded-full bg-lime-300 [animation-delay:300ms]" />
                    {loadingElapsed < 4
                      ? "Aryan is thinking"
                      : loadingElapsed < 25
                      ? "Waking up GPU container & loading model (~25s)..."
                      : "Generating response..."}
                  </div>
                )}
                <div ref={messagesEndRef} />
              </div>
            )}
          </div>

          <form onSubmit={sendMessage} className="mx-4 mb-4 rounded-2xl border border-white/10 bg-black/20 p-2 sm:mx-auto sm:mb-6 sm:w-[calc(100%-10rem)]">
            <textarea
              value={input}
              onChange={(event) => setInput(event.target.value)}
              onFocus={() => {
                if (gpuWarm === false && !isWarmingUp) {
                  void handleWarmupGpu()
                }
              }}
              onKeyDown={handleKeyDown}
              rows={2}
              placeholder="Message Aryan AI…"
              className="w-full resize-none bg-transparent px-3 py-2 text-sm outline-none placeholder:text-white/30"
            />
            <div className="flex items-center justify-between px-2 pb-1">
              <span className="hidden text-xs text-white/30 sm:inline">Enter to send · Shift + Enter for newline</span>
              <div className="ml-auto">
                <button type="submit" disabled={!input.trim() || isLoading} aria-label="Send message" className="grid size-9 place-items-center rounded-xl bg-lime-400 text-black transition hover:bg-lime-300 disabled:bg-white/10 disabled:text-white/25">
                  <ArrowUp size={18} />
                </button>
              </div>
            </div>
          </form>
        </section>
      </div>
    </main>
  )
}
