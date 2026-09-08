"""Verify 节点：验证候选 Recipe 能否真的解析出岗位，失败则驱动 LLM 自修正。

对齐 Go 侧 explorer_agents 的 verifyAndRefine 闭环：

    探索（含真实网络观测）→ 产出配置 → 用「已观测响应」独立试解析
      → 解析出岗位（JobsFound>0 且 title 字段可达）即通过
      → 否则把「错误 + 实际响应样本」喂回 LLM 让它自己改配置
      → 最多 max_refine_rounds 轮

为什么用「已观测响应」而非重新发请求：
    快手这类站点需要登录态/cookie，纯 HTTP 复现会 403。而 browser-use
    探索时已经带着登录态拿到了真实接口响应（见 controller.capture_requests），
    直接解析这份响应既不额外发请求、也不依赖 cookie —— 对齐 Go 的
    verifyWithObservedResponse（BrowserBound=true）路径。
"""
import json
from typing import List, Optional

from app import config
from app.schemas.recipe import RecipeCandidate

# 自修正最大轮数（对齐 Go maxRefineRounds=3）
MAX_REFINE_ROUNDS = 3

# 验证时的兜底关键词：用户可能没给关键词，但无关键词时列表接口可能返回空，
# 会被误判为配置错误。用宽泛通用词让验证聚焦「路径与方式对不对」。
VERIFY_KEYWORD_FALLBACK = "工程师"


def _resolve(obj, path: str):
    """按点路径解析 JSON。'data.list' -> obj['data']['list']。

    返回 (value, hit) —— hit 表示路径是否成功取到值（用于判 title 字段是否可达）。
    """
    cur = obj
    for part in path.split("."):
        if isinstance(cur, dict) and part in cur:
            cur = cur[part]
        elif isinstance(cur, list) and part.isdigit() and cur:
            cur = cur[int(part)]
        else:
            return None, False
    return cur, True


def _parse_jobs(cand: RecipeCandidate, body: str):
    """用 recipe 的 list_path/title_field 解析一份已观测响应，返回 (jobs, title_hit)。

    对齐 Go 的 jobsFromObservedResponse：只消费已观测响应，不发起额外请求。
    """
    try:
        data = json.loads(body)
    except (ValueError, TypeError):
        return [], False

    # 找到与 candidate.list_api 同 host+path 的观测响应（宽松匹配：取第一个 JSON 响应兜底）
    node, path_ok = _resolve(data, cand.list_path)
    if not path_ok or not isinstance(node, list):
        return [], False

    jobs = []
    title_hit = False
    for item in node:
        if not isinstance(item, dict):
            continue
        title_val, ok = _resolve(item, cand.title_field)
        if ok and title_val not in (None, ""):
            title_hit = True
        jobs.append({"title": title_val if ok else None})
    return jobs, title_hit


def verify_with_observed(cand: RecipeCandidate, observed: List[dict]) -> dict:
    """用已观测响体验证 candidate。返回与 Go executor.VerifyResult 同义的 dict。

    {
        "ok": bool,
        "jobs_found": int,
        "error": str,
        "sample_titles": List[str],
    }
    """
    if not cand or not cand.list_api:
        return {"ok": False, "jobs_found": 0, "error": "候选配置为空（list_api 缺失）"}
    if not observed:
        return {"ok": False, "jobs_found": 0, "error": "无已观测响应可复验（browser-use 未抓到列表接口）"}

    for resp in observed:
        url = resp.get("url", "")
        # 仅对与候选 list_api 同源的 JSON 响应做解析（避免误用无关接口）
        if cand.list_api.split("?")[0] not in url and url.split("?")[0] not in cand.list_api:
            # 宽松：host+path 任一包含对方即尝试
            if not (_host_path_match(cand.list_api, url)):
                continue
        jobs, title_hit = _parse_jobs(cand, resp.get("body", ""))
        if jobs:
            titles = [j["title"] for j in jobs[:3] if j["title"]]
            return {
                "ok": True,
                "jobs_found": len(jobs),
                "error": "",
                "sample_titles": titles,
                "browser_bound": True,
                "used_url": url,
            }
    return {
        "ok": False,
        "jobs_found": 0,
        "error": "已观测响应中未能按 list_path 解析出岗位列表（路径/字段可能写错）",
    }


def _host_path_match(a: str, b: str) -> bool:
    """对齐 Go sameObservedEndpoint：host+path 相同即视为同一接口。"""
    import urllib.parse as up

    try:
        ua, ub = up.urlparse(a), up.urlparse(b)
        return ua.netloc == ub.netloc and ua.path == ub.path and ua.netloc != ""
    except Exception:
        return a.strip() == b.strip()


def refine_recipe(cand: RecipeCandidate, observed: List[dict], error: str) -> Optional[RecipeCandidate]:
    """把失败原因 + 实际观测响应喂回 LLM，让它自修正 list_path/字段等。

    返回修正后的 RecipeCandidate，或 None（模型无新思路）。
    """
    from app.llm import get_llm
    from langchain_core.messages import HumanMessage, SystemMessage

    # 取一个相关观测响应作样本（截断，避免超长）
    sample = ""
    for resp in observed:
        if _host_path_match(cand.list_api, resp.get("url", "")):
            sample = resp.get("body", "")[:4000]
            break
    if not sample:
        sample = (observed[0].get("body", "") if observed else "")[:4000]

    system = (
        "你是校招站点接口解析配置的修正器。下面是一份候选 Recipe 与一次验证失败的原因，"
        "以及浏览器实际观测到的接口响应样本。请根据响应样本，修正 list_path / title_field / "
        "id_field / field_map 等字段路径，使按 list_path 能取到岗位数组、title_field 能取到标题。"
        "只输出 JSON，字段同 RecipeCandidate。"
    )
    user = (
        f"失败原因：{error}\n\n"
        f"当前候选：{cand.model_dump_json()}\n\n"
        f"观测响应样本（请据此修正路径）：\n{sample}\n"
    )
    llm = get_llm()
    resp = llm.invoke([SystemMessage(content=system), HumanMessage(content=user)])
    text = (resp.content if isinstance(resp.content, str) else str(resp.content))
    # 兼容 ```json 包裹
    text = text.strip().strip("`")
    if text.startswith("json"):
        text = text[4:].strip()
    try:
        data = json.loads(text)
    except (ValueError, TypeError):
        return None
    # 仅保留与执行结果有关的字段，避免 notes/confidence 干扰
    keep = {k: v for k, v in data.items() if k in RecipeCandidate.model_fields}
    if not keep.get("list_api"):
        return None
    refined = RecipeCandidate(**keep)
    # 模型交回完全相同配置说明已无新思路
    if (refined.list_api == cand.list_api and refined.method == cand.method
            and refined.list_path == cand.list_path and refined.title_field == cand.title_field
            and refined.list_api and refined.id_field == cand.id_field
            and refined.request_body == cand.request_body and refined.keyword_param == cand.keyword_param):
        return None
    return refined


def same_candidate(a: RecipeCandidate, b: RecipeCandidate) -> bool:
    return (a.list_api == b.list_api and a.method == b.method
            and a.request_body == b.request_body and a.request_content_type == b.request_content_type
            and a.list_path == b.list_path and a.title_field == b.title_field
            and a.id_field == b.id_field and a.keyword_param == b.keyword_param
            and a.keyword_in_body == b.keyword_in_body)
