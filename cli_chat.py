import sys
import modal

AVAILABLE_MODELS = {
    "1": ("qwen-6k", "Qwen 2.5 14B — Balanced 6k Persona", 0.7, "Best all-around Aryan texting persona"),
    "2": ("qwen-comedy", "Qwen 2.5 14B — Comedy & Banter Specialist", 0.8, "Punchy jokes, laugh reactions & sharp banter"),
    "3": ("llama-v3", "Llama 3.1 8B — Balanced v3", 0.7, "Previous Llama 3.1 8B balanced model"),
    "4": ("llama-v2", "Llama 3 8B — v2 Baseline", 0.7, "Initial 8B baseline fine-tune"),
}

TAG_TO_KEY = {v[0]: k for k, v in AVAILABLE_MODELS.items()}

def print_header(current_tag: str, temp: float):
    tag, name, _, desc = [v for v in AVAILABLE_MODELS.values() if v[0] == current_tag][0]
    print("\n" + "=" * 68)
    print(f"  💬  Aryan AI Interactive Chatroom")
    print(f"  🤖  Active Model: {name} ({tag})")
    print(f"  🎯  Focus:        {desc}")
    print(f"  ⚙️   Temperature:  {temp:.2f}")
    print("-" * 68)
    print("  Commands:")
    print("    /model        - Switch model (preserves or resets history)")
    print("    /models       - List all available models")
    print("    /temp <val>   - Set temperature (e.g. /temp 0.85)")
    print("    /clear        - Clear conversation memory")
    print("    /history      - Print current conversation context")
    print("    /quit or exit - Exit chat")
    print("=" * 68 + "\n")

def select_model(current_tag: str):
    print("\n" + "─" * 50)
    print("  Select Model to Chat With:")
    print("─" * 50)
    for key, (tag, name, default_t, desc) in AVAILABLE_MODELS.items():
        active = " [ACTIVE]" if tag == current_tag else ""
        print(f"  [{key}] {name}{active}")
        print(f"      Tag: {tag:<12} | {desc}")
    print("─" * 50)
    
    choice = input("Enter number (1-4) or press Enter to cancel: ").strip().lower()
    if not choice:
        return None, None
    
    # Check if choice is number or tag
    if choice in AVAILABLE_MODELS:
        new_tag = AVAILABLE_MODELS[choice][0]
    elif choice in TAG_TO_KEY:
        new_tag = choice
    else:
        print("Invalid choice.")
        return None, None
    
    if new_tag == current_tag:
        print(f"Already using {new_tag}.")
        return None, None
        
    keep = input("Keep existing conversation history with new model? [Y/n]: ").strip().lower()
    keep_history = keep not in ["n", "no", "clear"]
    
    return new_tag, keep_history

def main():
    current_tag = "qwen-6k"
    current_temp = 0.7

    print("=" * 68)
    print("  Connecting to Modal Cloud backend (aryan-inference)...")
    print("=" * 68)

    try:
        ChatModel = modal.Cls.from_name("aryan-inference", "ChatModel")
    except Exception as e:
        print(f"\n❌ Could not connect to deployed 'aryan-inference' app: {e}")
        print("Deploying it first with: modal deploy backend/inference.py\n")
        return

    def get_family(tag):
        return "qwen" if "qwen" in (tag or "").lower() else "llama"

    # Cache bot references per architecture family so we reuse warm GPU containers
    bots = {}
    
    def get_bot(tag):
        family = get_family(tag)
        if family not in bots:
            bots[family] = ChatModel(model_tag=family)
        return bots[family]

    bot = get_bot(current_tag)

    # Initial warmup
    sys.stdout.write(f"Waking up {current_tag} container on GPU...\r")
    sys.stdout.flush()
    try:
        bot.warmup.remote()
    except Exception as e:
        print(f"(Warmup note: {e})")

    print_header(current_tag, current_temp)

    history = []

    while True:
        try:
            user_input = input("You: ").strip()
        except (KeyboardInterrupt, EOFError):
            print("\nBye!")
            break

        if not user_input:
            continue

        # ── Slash commands ───────────────────────────────────
        cmd = user_input.lower()

        if cmd in ["quit", "exit", "/quit", "/exit", ":q"]:
            print("Bye!")
            break

        if cmd in ["/clear", "clear"]:
            history = []
            print("\n[🧹 Chat memory cleared]\n")
            continue

        if cmd in ["/models", "models"]:
            print("\nAvailable Models:")
            for key, (tag, name, default_t, desc) in AVAILABLE_MODELS.items():
                active = " 👉 (Active)" if tag == current_tag else ""
                print(f"  [{key}] {name}{active}\n      Tag: {tag} | {desc}")
            print()
            continue

        if cmd in ["/model", "/switch", "switch", "model"]:
            new_tag, keep_history = select_model(current_tag)
            if new_tag:
                current_tag = new_tag
                bot = get_bot(current_tag)
                # Auto-adjust default temp for comedy if user hasn't changed it
                for t, n, dt, _ in AVAILABLE_MODELS.values():
                    if t == current_tag:
                        current_temp = dt
                if not keep_history:
                    history = []
                    print("\n[🧹 Switched model and reset conversation memory]")
                else:
                    print(f"\n[🔄 Switched model to {current_tag} — continuing previous conversation]")
                
                sys.stdout.write(f"Warming up {current_tag}...\r")
                sys.stdout.flush()
                try:
                    bot.warmup.remote()
                except Exception:
                    pass
                print_header(current_tag, current_temp)
            continue

        if cmd.startswith("/temp"):
            parts = user_input.split()
            if len(parts) > 1:
                try:
                    val = float(parts[1])
                    if 0.1 <= val <= 1.5:
                        current_temp = val
                        print(f"[Temperature set to {current_temp:.2f}]\n")
                    else:
                        print("[Temperature must be between 0.1 and 1.5]\n")
                except ValueError:
                    print("[Invalid number for temperature]\n")
            else:
                print(f"[Current temperature: {current_temp:.2f}]\n")
            continue

        if cmd in ["/history", "history"]:
            print("\n--- Current Context History ---")
            if not history:
                print("(empty)")
            for h in history:
                role = "Aryan" if h["role"] == "assistant" else "You"
                print(f"{role}: {h['content']}")
            print("-------------------------------\n")
            continue

        # ── Regular chat turn ────────────────────────────────
        history.append({"role": "user", "content": user_input})

        # Trim context to last 12 turns for fast, snappy replies
        if len(history) > 12:
            history = history[-12:]

        sys.stdout.write(f"Aryan ({current_tag}): ...\r")
        sys.stdout.flush()

        try:
            response = bot.generate.remote(
                history=history,
                model=current_tag,
                temperature=current_temp,
                top_p=0.9,
                max_new_tokens=256,
            )
            print(f"Aryan ({current_tag}): {response}\n")
            history.append({"role": "assistant", "content": response})
        except Exception as e:
            print(f"\n❌ Error from Modal: {e}\n")

if __name__ == "__main__":
    main()
