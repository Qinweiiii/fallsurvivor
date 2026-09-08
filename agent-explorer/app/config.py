"""配置：从环境变量读取，不引入额外依赖。"""
import os


class Settings:
    def __init__(self) -> None:
        # 通义千问（dashscope OpenAI 兼容端点）。优先 QWEN_API_KEY；
        # 兼容历史：若未设 QWEN_API_KEY 但设了 DEEPSEEK_API_KEY（实际是 dashscope key），则复用之。
        self.qwen_api_key = os.getenv("QWEN_API_KEY") or os.getenv("DEEPSEEK_API_KEY", "")
        self.qwen_base_url = os.getenv("QWEN_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1")
        self.qwen_model = os.getenv("QWEN_MODEL", "qwen3.8-max")
        self.browser_worker_token = os.getenv("BROWSER_WORKER_TOKEN", "")
        self.offline = os.getenv("OFFLINE", "true").lower() in ("1", "true", "yes")
        self.max_steps = int(os.getenv("MAX_STEPS", "12"))
        self.confidence_threshold = int(os.getenv("CONFIDENCE_THRESHOLD", "70"))
        self.host = os.getenv("HOST", "0.0.0.0")
        self.port = int(os.getenv("PORT", "8400"))
        self.profile_dir = os.getenv("PROFILE_DIR", "../worker-browser/.auth")


settings = Settings()
