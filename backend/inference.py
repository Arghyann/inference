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
    scaledown_window=120,         # Idle container lives for 2m to minimize GPU cost
    max_containers=1,             # Cap at 1 GPU shared across backend clients
)
class ChatModel:
    model_tag: str = modal.parameter(default="qwen-6k")

    @modal.enter()
    def load_model(self):
        from unsloth import FastLanguageModel

        MODEL_PATHS = {
            "qwen-6k": "/data/aryan-qwen14b-6k-lora",
            "qwen-comedy": "/data/aryan-qwen14b-comedy-lora",
            "llama-v3": "/data/aryan-llama-lora-v3",
            "llama-v2": "/data/aryan-llama-lora-v2",
        }

        # Normalize tag and handle legacy aliases
        tag = (self.model_tag or "qwen-6k").lower().strip()
        if tag in ["v3", "llama-3"]:
            tag = "llama-v3"
        elif tag in ["v2", "v1", "llama-2"]:
            tag = "llama-v2"
        elif tag not in MODEL_PATHS:
            tag = "qwen-6k"

        checkpoint_path = MODEL_PATHS[tag]
        print(f"Loading model '{tag}' from {checkpoint_path}...")

        self.model, self.tokenizer = FastLanguageModel.from_pretrained(
            model_name=checkpoint_path,
            max_seq_length=2048,
            dtype=None,
            load_in_4bit=True,
        )

        FastLanguageModel.for_inference(self.model)
        print(f"Model '{tag}' loaded into GPU VRAM and ready!")

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

        # Default comedy temperature to 0.8 if standard 0.7 was passed
        if self.model_tag == "qwen-comedy" and temperature == 0.7:
            temperature = 0.8

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
        return self.tokenizer.decode(response_tokens, skip_special_tokens=True).strip()
