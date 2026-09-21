"use client"

import { FormEvent, KeyboardEvent, useEffect, useRef, useState } from "react"
import { ArrowUp, ChevronDown, LogOut, MessageSquare, PanelLeft, Plus, Sparkles, Trash2, UserRound, X, Zap } from "lucide-react"
import { api, ChatMessage, Conversation, ModelTag } from "@/lib/api"

type AuthMode = "login" | "register"

export const MODEL_OPTIONS: {
  id: ModelTag
  label: string
  short: string
  badgeColor: string
  description: string
}[] = [
  {
    id: "qwen-6k",
    label: "Qwen 6k",
    short: "Qwen 6k",
    badgeColor: "text-lime-400",
    description: "Best all-around Aryan texting persona",
  },
  {
    id: "qwen-comedy",
    label: "Qwen Funny",
    short: "Qwen Funny",
    badgeColor: "text-emerald-400",
    description: "Punchy jokes, laugh reactions & sharp banter",
  },
  {
    id: "llama-v3",
    label: "Llama v3",
    short: "Llama v3",
    badgeColor: "text-cyan-400",
    description: "Previous Llama 3.1 8B balanced model",
  },
  {
    id: "llama-v2",
    label: "Llama v2",
    short: "Llama v2",
    badgeColor: "text-amber-300",
    description: "Initial 8B baseline fine-tune",
  },
]

export function getModelBadge(modelTag?: string) {
  switch (modelTag) {
    case "qwen-comedy":
      return { label: "Qwen Funny", color: "text-emerald-400" }
    case "llama-v3":
    case "v3":
      return { label: "Llama v3", color: "text-cyan-400" }
    case "llama-v2":
    case "v2":
    case "v1":
      return { label: "Llama v2", color: "text-amber-300" }
    case "qwen-6k":
    default:
      return { label: "Qwen 6k", color: "text-lime-400" }
  }
}

export default function ChatbotUI() {
  const [authenticated, setAuthenticated] = useState(false)
  const [username, setUsername] = useState("")
  const [conversations, setConversations] = useState<Conversation[]>([])
  const [activeConversationId, setActiveConversationId] = useState<string | null>(null)
  const [isSidebarOpen, setIsSidebarOpen] = useState(true)
  const [isConversationsLoading, setIsConversationsLoading] = useState(false)
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
  const [selectedModel, setSelectedModel] = useState<ModelTag>("qwen-6k")
  const [promptLimit, setPromptLimit] = useState<number | null>(null)
  const [promptsUsed, setPromptsUsed] = useState<number>(0)
  const messagesEndRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const handleSignedOut = () => {
      setAuthenticated(false)
      setConversations([])
      setActiveConversationId(null)
      setMessages([])
      setUsername("")
      setPromptLimit(null)
      setPromptsUsed(0)
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

    // 0. Fetch user profile and message quota
    api.getUserMe()
      .then((me) => {
        setPromptLimit(me.prompt_limit)
        setPromptsUsed(me.prompts_used)
      })
      .catch(() => {})

    // 1. Fetch user conversations
    setIsConversationsLoading(true)
    api.getConversations()
      .then((convs) => {
        setConversations(convs)
        if (convs.length > 0) {
          setActiveConversationId(convs[0].id)
          setIsHistoryLoading(true)
          api.getConversationMessages(convs[0].id)
            .then(setMessages)
            .catch(() => setAuthError("Could not load conversation messages."))
            .finally(() => setIsHistoryLoading(false))
        } else {
          setActiveConversationId(null)
          setMessages([])
        }
      })
      .catch(() => setAuthError("Could not load your conversations."))
      .finally(() => setIsConversationsLoading(false))

    // 2. Check GPU status on load
    api.getGpuStatus()
      .then((res) => setGpuWarm(res.warm))
      .catch(() => {})

    // 3. Periodic GPU status check
    const interval = setInterval(() => {
      api.getGpuStatus()
        .then((res) => setGpuWarm(res.warm))
        .catch(() => {})
    }, 25000)

    // 4. Keyboard shortcut: Cmd+K / Ctrl+K to start New Chat
    const handleGlobalKeyDown = (e: globalThis.KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault()
        handleNewChat()
      }
    }
    window.addEventListener("keydown", handleGlobalKeyDown)

    return () => {
      clearInterval(interval)
      window.removeEventListener("keydown", handleGlobalKeyDown)
    }
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
      await api.warmup(selectedModel)
      setGpuWarm(true)
    } catch (error) {
      console.error("GPU warmup failed:", error)
    } finally {
      setIsWarmingUp(false)
    }
  }

  function handleNewChat() {
    setActiveConversationId(null)
    setMessages([])
    setAuthError("")
    if (typeof window !== "undefined" && window.innerWidth < 768) {
      setIsSidebarOpen(false)
    }
  }

  async function handleSelectConversation(id: string) {
    if (activeConversationId === id) {
      if (typeof window !== "undefined" && window.innerWidth < 768) {
        setIsSidebarOpen(false)
      }
      return
    }
    setActiveConversationId(id)
    setIsHistoryLoading(true)
    if (typeof window !== "undefined" && window.innerWidth < 768) {
      setIsSidebarOpen(false)
    }
    try {
      const msgs = await api.getConversationMessages(id)
      setMessages(msgs)
    } catch {
      setAuthError("Failed to load conversation.")
    } finally {
      setIsHistoryLoading(false)
    }
  }

  async function handleDeleteConversation(event: React.MouseEvent, id: string) {
    event.stopPropagation()
    try {
      await api.deleteConversation(id)
      const remaining = conversations.filter((c) => c.id !== id)
      setConversations(remaining)
      if (activeConversationId === id) {
        if (remaining.length > 0) {
          void handleSelectConversation(remaining[0].id)
        } else {
          handleNewChat()
        }
      }
    } catch {
      setAuthError("Failed to delete conversation.")
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
        if (result.prompt_limit !== undefined) setPromptLimit(result.prompt_limit)
        if (result.prompts_used !== undefined) setPromptsUsed(result.prompts_used)
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
      const response = await api.sendMessage(content, activeConversationId || undefined, selectedModel)
      setMessages((current) => [
        ...current,
        {
          role: "assistant",
          content: response.reply,
          model: response.model || selectedModel,
          created_at: response.created_at,
        },
      ])
      setGpuWarm(true)

      if (response.prompts_used !== undefined) setPromptsUsed(response.prompts_used)
      if (response.prompt_limit !== undefined) setPromptLimit(response.prompt_limit)

      // If we were on a brand new chat, set active ID and refresh conversation list
      if (!activeConversationId && response.conversation_id) {
        setActiveConversationId(response.conversation_id)
        api.getConversations().then(setConversations).catch(() => {})
      }
    } catch (error) {
      setInput(content)
      setMessages((current) => [
        ...current,
        {
          role: "assistant",
          content: error instanceof Error ? error.message : "Something went wrong. Please try again.",
          model: selectedModel,
          created_at: new Date().toISOString(),
        },
      ])
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

  const activeConversation = conversations.find((c) => c.id === activeConversationId)
  const activeTitle = activeConversation ? activeConversation.title : "Aryan AI"

  return (
    <main className="min-h-screen bg-[#090b0a] p-2 sm:p-4 md:p-6 text-white flex items-center justify-center">
      <div className="flex h-[calc(100vh-1rem)] sm:h-[calc(100vh-2rem)] md:h-[calc(100vh-3rem)] w-full max-w-6xl overflow-hidden rounded-[24px] sm:rounded-[28px] border border-white/10 bg-white/[0.035] shadow-2xl shadow-black/50 backdrop-blur-xl relative">
        
        {/* Mobile Backdrop */}
        {isSidebarOpen && (
          <div
            className="fixed inset-0 z-40 bg-black/70 backdrop-blur-sm md:hidden"
            onClick={() => setIsSidebarOpen(false)}
          />
        )}

        {/* Sidebar */}
        <aside
          className={`
            fixed inset-y-0 left-0 z-50 flex w-72 flex-col border-r border-white/10 bg-[#0c0e0d]/95 backdrop-blur-2xl transition-all duration-300 ease-in-out
            md:static md:z-auto md:bg-black/25
            ${isSidebarOpen ? "translate-x-0" : "-translate-x-full md:hidden"}
          `}
        >
          {/* Sidebar Top: Logo + Close */}
          <div className="p-4 border-b border-white/10 flex items-center justify-between">
            <div className="flex items-center gap-2.5">
              <div className="grid size-8 place-items-center rounded-xl bg-lime-400 text-black font-bold text-sm">
                A
              </div>
              <div>
                <h2 className="font-semibold text-sm tracking-tight text-white">Aryan AI</h2>
                <p className="text-[11px] text-white/40">Custom LLaMA Chat</p>
              </div>
            </div>
            <button
              type="button"
              onClick={() => setIsSidebarOpen(false)}
              className="md:hidden rounded-lg p-1.5 text-white/40 hover:bg-white/10 hover:text-white"
            >
              <X size={18} />
            </button>
          </div>

          {/* New Chat Button */}
          <div className="p-3">
            <button
              type="button"
              onClick={handleNewChat}
              className="flex w-full items-center justify-between gap-2 rounded-xl border border-lime-400/30 bg-lime-400/10 px-3.5 py-2.5 text-sm font-semibold text-lime-300 transition hover:bg-lime-400/20 active:scale-[0.98]"
            >
              <div className="flex items-center gap-2">
                <Plus size={16} />
                <span>New Chat</span>
              </div>
              <span className="text-[11px] uppercase tracking-wider text-lime-400/60 font-mono">⌘K</span>
            </button>
          </div>

          {/* Conversation List */}
          <div className="flex-1 overflow-y-auto px-3 py-2 space-y-1">
            <div className="px-2 pb-1.5 text-[11px] font-medium uppercase tracking-wider text-white/35">
              Your Chats
            </div>
            {isConversationsLoading ? (
              <div className="py-6 text-center text-xs text-white/30">Loading chats…</div>
            ) : conversations.length === 0 ? (
              <div className="rounded-xl border border-dashed border-white/10 p-4 text-center text-xs text-white/30">
                No previous chats yet. Start a new chat!
              </div>
            ) : (
              conversations.map((conv) => (
                <div
                  key={conv.id}
                  onClick={() => void handleSelectConversation(conv.id)}
                  className={`group flex items-center justify-between gap-2 rounded-xl px-3 py-2.5 text-sm transition cursor-pointer ${
                    activeConversationId === conv.id
                      ? "bg-white/[0.09] text-white border border-white/10 shadow-sm"
                      : "text-white/60 hover:bg-white/[0.04] hover:text-white/90 border border-transparent"
                  }`}
                >
                  <div className="flex items-center gap-2.5 min-w-0 flex-1">
                    <MessageSquare
                      size={15}
                      className={activeConversationId === conv.id ? "text-lime-400 shrink-0" : "text-white/40 shrink-0"}
                    />
                    <span className="truncate text-xs sm:text-sm font-medium">{conv.title}</span>
                  </div>
                  <button
                    type="button"
                    onClick={(e) => void handleDeleteConversation(e, conv.id)}
                    className="opacity-0 group-hover:opacity-100 rounded p-1 text-white/40 hover:text-red-400 transition hover:bg-white/10 shrink-0"
                    title="Delete chat"
                  >
                    <Trash2 size={13} />
                  </button>
                </div>
              ))
            )}
          </div>

          {/* Sidebar Footer: User profile + Quota + Logout */}
          <div className="p-3 border-t border-white/10 mt-auto space-y-2">
            {promptLimit !== null && (
              <div className="flex items-center justify-between px-1 text-[11px] font-mono">
                <span className="text-white/40">Message Quota:</span>
                <span className={promptLimit > 0 && promptsUsed >= promptLimit ? "text-red-400 font-semibold" : "text-lime-300 font-medium"}>
                  {promptLimit <= 0 ? "Unlimited" : `${Math.max(0, promptLimit - promptsUsed)} / ${promptLimit} left`}
                </span>
              </div>
            )}
            <button
              type="button"
              onClick={() => api.logout()}
              className="flex w-full items-center justify-between rounded-xl px-3 py-2 text-xs text-white/60 transition hover:bg-white/10 hover:text-white"
            >
              <div className="flex items-center gap-2 min-w-0">
                <UserRound size={14} className="shrink-0" />
                <span className="truncate font-medium">{username}</span>
              </div>
              <LogOut size={14} className="shrink-0" />
            </button>
          </div>
        </aside>

        {/* Main Chat Panel */}
        <section className="flex flex-1 flex-col overflow-hidden">
          {/* Header */}
          <header className="flex items-center justify-between border-b border-white/10 px-4 py-3 sm:px-6 sm:py-4">
            <div className="flex items-center gap-2.5 min-w-0">
              <button
                type="button"
                onClick={() => setIsSidebarOpen((prev) => !prev)}
                className="grid size-9 place-items-center rounded-xl border border-white/10 bg-white/[0.04] text-white/70 hover:bg-white/10 hover:text-white transition shrink-0"
                title="Toggle sidebar"
              >
                <PanelLeft size={17} />
              </button>
              <button
                type="button"
                onClick={handleNewChat}
                className="flex items-center gap-1.5 rounded-xl border border-lime-400/30 bg-lime-400/10 px-2.5 py-1.5 text-xs font-semibold text-lime-300 hover:bg-lime-400/20 transition shrink-0"
                title="Start a new chat (⌘K)"
              >
                <Plus size={14} />
                <span className="hidden sm:inline">New Chat</span>
              </button>
              <div className="min-w-0 ml-1">
                <h1 className="truncate font-semibold tracking-tight text-sm sm:text-base">
                  {activeTitle}
                </h1>
                <p className="text-[11px] text-white/40">Fine-tuned assistant · online</p>
              </div>
            </div>

            <div className="flex items-center gap-2 shrink-0">
              {/* Message Quota Pill */}
              {promptLimit !== null && (
                <div
                  className={`hidden sm:flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-mono ${
                    promptLimit > 0 && promptsUsed >= promptLimit
                      ? "border-red-400/30 bg-red-400/10 text-red-300"
                      : "border-white/10 bg-white/[0.04] text-white/70"
                  }`}
                  title={promptLimit <= 0 ? "Unlimited message quota" : `${promptsUsed} of ${promptLimit} messages used`}
                >
                  <span className="text-[10px]">💬</span>
                  <span>{promptLimit <= 0 ? "Unlimited" : `${Math.max(0, promptLimit - promptsUsed)} left`}</span>
                </div>
              )}

              {/* Model Dropdown */}
              <div className="relative">
                <select
                  value={selectedModel}
                  onChange={(e) => setSelectedModel(e.target.value as ModelTag)}
                  aria-label="Select AI Model Version"
                  className="h-8 appearance-none rounded-full border border-white/15 bg-white/[0.06] pl-3 pr-7 text-xs font-medium text-white transition hover:bg-white/[0.1] focus:border-lime-400/60 focus:outline-none cursor-pointer"
                >
                  {MODEL_OPTIONS.map((opt) => (
                    <option key={opt.id} value={opt.id} className="bg-[#121513] text-white">
                      {opt.label}
                    </option>
                  ))}
                </select>
                <ChevronDown className="pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2 size-3 text-white/50" />
              </div>

              {isWarmingUp ? (
                <div className="flex items-center gap-2 rounded-full border border-amber-400/30 bg-amber-400/10 px-3 py-1.5 text-xs text-amber-300">
                  <span className="size-2 animate-ping rounded-full bg-amber-400" />
                  <span className="hidden sm:inline">Waking GPU (~25s)...</span>
                  <span className="sm:hidden">Waking...</span>
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
            </div>
          </header>

          {/* Messages List */}
          <div className="flex-1 overflow-y-auto px-4 py-6 sm:px-12 sm:py-8">
            {isHistoryLoading ? (
              <div className="flex items-center justify-center py-20 text-sm text-white/40">Loading messages…</div>
            ) : messages.length === 0 ? (
              <div className="mx-auto flex max-w-lg flex-col items-center justify-center py-20 text-center">
                <div className="mb-5 grid size-14 place-items-center rounded-2xl border border-lime-300/20 bg-lime-300/10 text-lime-300">
                  <Sparkles />
                </div>
                <h2 className="text-2xl font-semibold">What can I help with?</h2>
                <p className="mt-2 text-sm text-white/45">
                  Ask Aryan AI anything. Active persona:{" "}
                  <span className={`${MODEL_OPTIONS.find((m) => m.id === selectedModel)?.badgeColor || "text-lime-400"} font-medium`}>
                    {MODEL_OPTIONS.find((m) => m.id === selectedModel)?.label || selectedModel}
                  </span>
                </p>
              </div>
            ) : (
              <div className="mx-auto max-w-2xl space-y-6">
                {messages.map((message, index) => {
                  const badge = getModelBadge(message.model)
                  return (
                    <div key={`${message.created_at}-${index}`} className={`flex gap-3 ${message.role === "user" ? "justify-end" : "justify-start"}`}>
                      <div className={`max-w-[85%] rounded-2xl px-4 py-3 text-sm leading-6 ${message.role === "user" ? "bg-lime-400 text-black" : "border border-white/10 bg-white/[0.06] text-white/85"}`}>
                        {message.role === "assistant" && (
                          <div className="mb-1 flex items-center gap-1.5 text-[10px] font-mono uppercase tracking-wider text-white/40">
                            <span>Aryan AI</span>
                            <span>•</span>
                            <span className={`${badge.color} font-semibold`}>
                              {badge.label}
                            </span>
                          </div>
                        )}
                        <p className="whitespace-pre-wrap">{message.content}</p>
                      </div>
                    </div>
                  )
                })}
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

          {/* Chat Input Form */}
          <form onSubmit={sendMessage} className="mx-4 mb-4 rounded-2xl border border-white/10 bg-black/30 p-2 sm:mx-auto sm:mb-6 sm:w-[calc(100%-6rem)]">
            {promptLimit !== null && promptLimit > 0 && promptsUsed >= promptLimit && (
              <div className="mb-2 rounded-xl border border-red-400/30 bg-red-400/10 px-3 py-2 text-xs text-red-200">
                You have reached your limit of {promptLimit} messages. Please contact Aryan to top up your quota.
              </div>
            )}
            <textarea
              value={input}
              disabled={isLoading || (promptLimit !== null && promptLimit > 0 && promptsUsed >= promptLimit)}
              onChange={(event) => setInput(event.target.value)}
              onFocus={() => {
                if (gpuWarm === false && !isWarmingUp) {
                  void handleWarmupGpu()
                }
              }}
              onKeyDown={handleKeyDown}
              rows={2}
              placeholder={
                promptLimit !== null && promptLimit > 0 && promptsUsed >= promptLimit
                  ? "Message quota reached. Contact Aryan for more access."
                  : "Message Aryan AI… (⌘K for New Chat)"
              }
              className="w-full resize-none bg-transparent px-3 py-2 text-sm outline-none placeholder:text-white/30 disabled:opacity-50 disabled:cursor-not-allowed"
            />
            <div className="flex items-center justify-between px-2 pb-1">
              <span className="hidden text-xs text-white/30 sm:inline">
                {promptLimit !== null && promptLimit > 0 && promptsUsed >= promptLimit
                  ? "Quota reached"
                  : "Enter to send · Shift + Enter for newline"}
              </span>
              <div className="ml-auto">
                <button
                  type="submit"
                  disabled={!input.trim() || isLoading || (promptLimit !== null && promptLimit > 0 && promptsUsed >= promptLimit)}
                  aria-label="Send message"
                  className="grid size-9 place-items-center rounded-xl bg-lime-400 text-black transition hover:bg-lime-300 disabled:bg-white/10 disabled:text-white/25"
                >
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
