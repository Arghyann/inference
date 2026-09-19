import modal

app = modal.App("aryan-inference")
volume = modal.Volume.from_name("aryan-finetune-vol")

image = (
    modal.Image.debian_slim(python_version="3.11")
    .pip_install(
        "unsloth",
        "transformers",
        "torch",
        "accelerate",
        "bitsandbytes"
    )
)

@app.cls(image=image, gpu="T4", volumes={"/data": volume}, scaledown_window=60)
class ChatModel:
    @modal.enter()
    def load_model(self):
        from unsloth import FastLanguageModel
        
        checkpoint_path = "/data/aryan-"
        print(f"Loading trained weights from {checkpoint_path}...")
        
        # This only runs ONCE when the container boots up!
        self.model, self.tokenizer = FastLanguageModel.from_pretrained(
            model_name=checkpoint_path,
            max_seq_length=1024,
            dtype=None,
            load_in_4bit=True,
        )
        FastLanguageModel.for_inference(self.model)
        print("Model loaded into VRAM and ready!")

    @modal.method()
    def generate(self, history: list):
        # We now pass the entire conversation history instead of a single prompt
        # Ensure the system prompt is at the very beginning
        messages = [
            {
                "role": "system", 
                "content": "You are Aryan. Respond exactly as Aryan would — in his natural texting style, casual mix of Hindi and English (Hinglish), short punchy replies, never formal. Never break character."
            }
        ] + history
        
        inputs = self.tokenizer.apply_chat_template(
            messages,
            tokenize=True,
            add_generation_prompt=True,
            return_tensors="pt"
        ).to("cuda")

        outputs = self.model.generate(
            input_ids=inputs,
            max_new_tokens=512,
            use_cache=True,
            temperature=0.7,
            top_p=0.9
        )
        
        response_tokens = outputs[0][len(inputs[0]):]
        return self.tokenizer.decode(response_tokens, skip_special_tokens=True)


@app.local_entrypoint()
def main():
    print("Booting up container and loading model into VRAM...")
    chat_bot = ChatModel()
    
    print("Welcome to your custom AI! Type 'quit' to exit.\n")
    
    # Keep track of the chat history so the AI has context, just like in training!
    chat_history = []
    
    while True:
        prompt = input("\nFriend: ")
        if prompt.lower() in ["quit", "exit"]:
            break
            
        # We format the prompt with a generic name prefix to match training style
        formatted_prompt = f"Friend: {prompt}"
        chat_history.append({"role": "user", "content": formatted_prompt})
        
        print("Thinking...")
        response = chat_bot.generate.remote(chat_history)
        
        print("\n=== ARYAN ===")
        print(response)
        print("===================\n")
        
        # Add the AI's response to the history so it remembers the context
        chat_history.append({"role": "assistant", "content": response})
