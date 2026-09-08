"""LLM 封装（生产 Planner / Actor 抽取用），懒加载 langchain，离线不依赖。

所有调用都走 DeepSeek（OpenAI 兼容）。离线模式（config.offline）下不应进入这里。
"""
from app import config


def get_llm(temperature: float = 0.0):
    """返回 langchain ChatOpenAI（通义千问 / dashscope 兼容）。仅在生产模式调用。"""
    from langchain_openai import ChatOpenAI

    if not config.settings.qwen_api_key:
        raise RuntimeError("生产模式需要 QWEN_API_KEY（见 agent-explorer/.env）")
    # dashscope 推理模型（如 qwen3.8-max）在 thinking 模式下不允许强制
    # tool_choice=required/object，而 browser-use 会强制 tool_choice 来驱动
    # action。关闭 thinking 模式后强制 tool_choice 即被允许。
    # 仅对 qwen 系列加此参数：DeepSeek 等 flash 模型无 thinking 模式冲突，
    # 且其兼容端点可能不支持该参数，带上反而 400。用空 dict 表示不加。
    extra_body: dict = {}
    if config.settings.qwen_model.startswith("qwen"):
        extra_body["enable_thinking"] = False
    return ChatOpenAI(
        model=config.settings.qwen_model,
        api_key=config.settings.qwen_api_key,
        base_url=config.settings.qwen_base_url,
        temperature=temperature,
        extra_body=extra_body or None,
    )


def chat_json(system: str, user: str, temperature: float = 0.0):
    """让模型产出 JSON，解析并返回 dict。失败抛错（由调用方做 Reflection/重试）。"""
    import json

    llm = get_llm(temperature)
    messages = [
        ("system", system + "\n只输出 JSON，不要任何解释性文字。"),
        ("human", user),
    ]
    raw = llm.invoke(messages).content
    text = raw.strip()
    # 容忍 ```json 包裹
    if text.startswith("```"):
        text = text.strip("`")
        if text.lower().startswith("json"):
            text = text[4:]
        text = text.strip()
    return json.loads(text)
