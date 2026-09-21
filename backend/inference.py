import os
import modal

# App and Volume bindings
app = modal.App("aryan-inference")
volume = modal.Volume.from_name("aryan-finetune-vol")

# Dedicated environment
image = (
    modal.Image.debian_slim(python_version="3.11")
    .pip_install(
        "unsloth",
        "transformers",
        "torch",
        "accelerate",
        "bitsandbytes",
    )
    .env({
        "HF_HOME": "/data/cache/huggingface"
    })
)

SYSTEM_PROMPT = (
    "You are Aryan. Respond exactly as Aryan would — in his natural "
    "texting style, casual mix of Hindi and English (Hinglish), short "
    "punchy replies, never formal. Never break character."
)

@app.cls(
    image=image,
    gpu="L4",                     # Matches finetune config: 24GB VRAM & fast inference
    volumes={"/data": volume},
    scaledown_window=300,         # Keep container warm for 5 minutes (matches chat.py)
    timeout=180,
    max_containers=1,             # Cap at 1 GPU shared across backend clients
)
class ChatModel:
    model_tag: str = modal.parameter(default="qwen")

    @modal.enter()
    def load_model(self):
        from unsloth import FastLanguageModel

        # Normalize family: "qwen" or "llama"
        family = "qwen" if "qwen" in (self.model_tag or "").lower() else "llama"

        if family == "qwen":
            checkpoint_base = "/data/aryan-qwen14b-6k-lora"
            checkpoint_aux = "/data/aryan-qwen14b-comedy-lora"
            aux_name = "comedy"
            print(f"Loading Qwen 2.5 14B base + 6k adapter from {checkpoint_base}...")
        else:
            checkpoint_base = "/data/aryan-llama-lora-v3"
            checkpoint_aux = "/data/aryan-llama-lora-v2"
            aux_name = "v2"
            print(f"Loading LLaMA 8B base + v3 adapter from {checkpoint_base}...")

        self.model, self.tokenizer = FastLanguageModel.from_pretrained(
            model_name=checkpoint_base,
            max_seq_length=2048,
            dtype=None,
            load_in_4bit=True,
        )

        try:
            print(f"Loading auxiliary adapter '{aux_name}' from {checkpoint_aux}...")
            self.model.load_adapter(checkpoint_aux, adapter_name=aux_name)
        except Exception as e:
            print(f"Notice loading auxiliary adapter: {e}")

        FastLanguageModel.for_inference(self.model)
        print(f"Model family '{family}' loaded into GPU VRAM with dual adapters ready!")

    @modal.method()
    def warmup(self) -> str:
        """Lightweight ping to wake container and execute @modal.enter() without token generation."""
        return f"{self.model_tag} ready"

    @modal.method()
    def generate(
        self,
        history: list,
        model: str = "",
        temperature: float = 0.7,
        top_p: float = 0.9,
        max_new_tokens: int = 256,
    ) -> str:
        import torch

        # Instant (<1ms) adapter switch on the same warm GPU
        req_model = (model or self.model_tag or "").lower()
        if "comedy" in req_model or "funny" in req_model:
            try:
                self.model.set_adapter("comedy")
            except Exception:
                self.model.set_adapter("default")
            if temperature == 0.7:
                temperature = 0.8
        elif "v2" in req_model or "v1" in req_model:
            try:
                self.model.set_adapter("v2")
            except Exception:
                self.model.set_adapter("default")
        else:
            try:
                self.model.set_adapter("default")
            except Exception:
                pass

        # Inject system prompt at the beginning of conversational history
        messages = [{"role": "system", "content": SYSTEM_PROMPT}] + history

        inputs = self.tokenizer.apply_chat_template(
            messages,
            tokenize=True,
            add_generation_prompt=True,
            return_tensors="pt",
        ).to("cuda")

        with torch.inference_mode():
            outputs = self.model.generate(
                input_ids=inputs,
                max_new_tokens=max_new_tokens,
                use_cache=True,
                temperature=temperature,
                top_p=top_p,
                pad_token_id=self.tokenizer.eos_token_id,
            )

        response_tokens = outputs[0][len(inputs[0]):]
        reply = self.tokenizer.decode(response_tokens, skip_special_tokens=True).strip()

        # Remove any accidental leading speaker labels
        for prefix in ["Aryan:", "Aryan AI:", "assistant:"]:
            if reply.startswith(prefix):
                reply = reply[len(prefix):].strip()

        # Stop sequence protection: cut off if model hallucinated another conversational turn
        for stop_marker in ["\nFriend:", "\nUser:", "\nHuman:", "\nAryan:", "\nassistant:", "\n<|im_end|>", "\n<|im_start|>"]:
            if stop_marker in reply:
                reply = reply.split(stop_marker)[0].strip()

        return reply
