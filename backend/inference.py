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
        # Cache HuggingFace base model weights directly in the mounted volume
        "HF_HOME": "/data/cache/huggingface"
    })
)

@app.cls(
    image=image,
    gpu="T4",                     # Lowest billing rate ($0.000164/sec)
    volumes={"/data": volume},
    scaledown_window=300,         # Shut down after 5m idle (prevents rapid cold starts)
    max_containers=1,             # Cap at 1 GPU; all users share the same warm container
)
class ChatModel:
    @modal.enter()
    def load_model(self):
        from unsloth import FastLanguageModel

        checkpoint_path = "/data/aryan-"

        # Loads base model (from /data/cache) + mounts LoRA adapter
        self.model, self.tokenizer = FastLanguageModel.from_pretrained(
            model_name=checkpoint_path,
            max_seq_length=2048,
            dtype=None,
            load_in_4bit=True,
        )
        FastLanguageModel.for_inference(self.model)

    @modal.method()
    def warmup(self) -> str:
        """Lightweight ping to wake container and execute @modal.enter() without token generation."""
        return "ready"

    @modal.method()
    def generate(self, history: list) -> str:
        import torch

        messages = [
            {
                "role": "system",
                "content": (
                    "You are Aryan. Respond exactly as Aryan would — in his natural "
                    "texting style, casual mix of Hindi and English (Hinglish), short "
                    "punchy replies, never formal. Never break character."
                ),
            }
        ] + history

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
