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
    gpu="T4",                     # Cost-effective T4 GPU ($0.000164/sec)
    volumes={"/data": volume},
    scaledown_window=300,         # Idle container lives for 5m to avoid cold starts
    max_containers=1,             # Cap at 1 GPU shared across backend clients
)
class ChatModel:
    @modal.enter()
    def load_model(self):
        from unsloth import FastLanguageModel

        # Base model with v2 adapter as default
        checkpoint_v2 = "/data/aryan-llama-lora-v2"
        checkpoint_v1 = "/data/aryan-"
        print(f"Loading base model + v2 adapter from {checkpoint_v2}...")

        self.model, self.tokenizer = FastLanguageModel.from_pretrained(
            model_name=checkpoint_v2,
            max_seq_length=2048,
            dtype=None,
            load_in_4bit=True,
        )

        print(f"Loading v1 adapter from {checkpoint_v1}...")
        self.model.load_adapter(checkpoint_v1, adapter_name="v1")

        FastLanguageModel.for_inference(self.model)
        print("Both v1 and v2 adapters loaded into single GPU VRAM and ready!")

    @modal.method()
    def warmup(self) -> str:
        """Lightweight ping to wake container and execute @modal.enter() without token generation."""
        return "ready"

    @modal.method()
    def generate(self, history: list, model: str = "v2") -> str:
        import torch

        # Switch active adapter instantly (<1ms) on the same GPU
        target_adapter = "v1" if model == "v1" else "default"
        try:
            self.model.set_adapter(target_adapter)
        except Exception as e:
            print(f"Error setting adapter to {target_adapter}, falling back to default: {e}")
            self.model.set_adapter("default")

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
                max_new_tokens=128,       # Optimized for punchy chat replies
                use_cache=True,
                temperature=0.7,
                top_p=0.9,
                pad_token_id=self.tokenizer.eos_token_id,
            )

        response_tokens = outputs[0][len(inputs[0]):]
        return self.tokenizer.decode(response_tokens, skip_special_tokens=True).strip()
