"""浏览器控制层 —— Actor 的真实实现所在（production）。

正式实现用 browser-use 驱动浏览器，复用你在 worker-browser/.auth 下手动登录过的
Chromium profile。browser-use 以 **pip 依赖** 装进本服务 venv，绝不 import 项目根目录的
./browser-use 本地克隆（那个文件夹仅供阅读、会被删）。所有 browser-use 使用代码写在
本文件（属于 fallsurvivor 自身）。

⚠️ 验证状态：本沙箱无法编译 browser-use 依赖（pydantic-core wheel 构建失败），因此
production 路径未在此环境真跑，仅按 browser-use 0.1.x 公开 API 编写。请在装有 browser-use
的机器上按 README「真机验证」步骤实测。所有 browser-use 引用均为懒加载，未安装时不影响
offline 测试与 import。
"""
import json
import os
import threading
from typing import Dict, List, Optional

from app import config
from app.schemas.recipe import RecipeCandidate

_BROWSER_LOCK = threading.Lock()
_BROWSER = None  # 跨单轮探索复用的 browser-use Browser 实例


def _abs_profile(site: str) -> Optional[str]:
    base = config.settings.profile_dir
    if not base:
        return None
    p = os.path.join(base, site)
    return p if os.path.isdir(p) else None


def _ensure_browser(site: str):
    """懒创建并复用 browser-use Browser（带登录 profile）。未安装时抛清晰错误。"""
    global _BROWSER
    if _BROWSER is not None:
        return _BROWSER
    try:
        from browser_use import Browser, BrowserConfig
    except ImportError as e:  # 未安装 browser-use：明确报错而非静默
        raise RuntimeError(
            "production Actor 需要 browser-use（pip install 'browser-use>=0.1,<0.2'），"
            "且需真实浏览器与登录 profile。本沙箱无法构建其依赖，未验证。"
        ) from e
    with _BROWSER_LOCK:
        if _BROWSER is None:
            profile = _abs_profile(site)
            headless = os.getenv("BROWSER_HEADLESS", "false").lower() in ("1", "true", "yes")
            cfg = BrowserConfig(headless=headless, user_data_dir=profile)
            _BROWSER = Browser(config=cfg)
    return _BROWSER


def close_browser() -> None:
    """一轮探索结束后关闭浏览器，释放 profile 锁。"""
    global _BROWSER
    with _BROWSER_LOCK:
        if _BROWSER is not None:
            try:
                _BROWSER.close()
            finally:
                _BROWSER = None


def _build_controller():
    """构建一个带 record_recipe / capture_requests 自定义动作的 Controller。

    - record_recipe：让 browser-use Agent 把发现的接口结构回传（核心产出）。
    - capture_requests：best-effort 用 Playwright 监听页面网络请求，供严谨校验。
    """
    from browser_use import Controller

    controller = Controller()
    # 注意：on_response 向 captured["responses"] 追加，record_recipe 写 captured["recipe"]，
    # list_captured / run_full_explore 读这两个键。必须一次性初始化齐全，否则 on_response 会
    # 因 KeyError 被下方 except 静默吞掉，导致响应永远进不了 dict（list_captured 恒为空）。
    captured: Dict[str, List[dict]] = {"requests": [], "responses": []}

    @controller.action("记录岗位列表接口结构")
    def record_recipe(
        list_api: str,
        method: str,
        id_field: str,
        title_field: str,
        list_path: str,
        keyword_param: str = "",
        detail_api: str = "",
        detail_url_template: str = "",
        request_body: str = "",
        request_content_type: str = "",
        request_headers: Optional[dict] = None,
        keyword_in_body: bool = False,
        field_map: Optional[dict] = None,
        notes: str = "",
        confidence: int = 0,
    ) -> str:
        captured["recipe"] = {
            "list_api": list_api,
            "detail_api": detail_api,
            "detail_url_template": detail_url_template,
            "method": method,
            "request_body": request_body,
            "request_content_type": request_content_type,
            "request_headers": request_headers or {},
            "id_field": id_field,
            "title_field": title_field,
            "list_path": list_path,
            "keyword_param": keyword_param,
            "keyword_in_body": keyword_in_body,
            "field_map": field_map or {},
            "notes": notes,
            "confidence": confidence,
        }
        return "已记录接口结构"

    @controller.action("捕获当前页面网络请求与列表接口响应")
    async def capture_requests(browser) -> str:
        # browser-use 注入给 action 的 browser 参数，类型是 browser-use 的 BrowserContext
        # 包装类（见 controller/registry/service.py:98,139）。它本身没有 .on；
        # 其底层 Playwright BrowserContext 存在 browser.session.context
        # （browser_use/browser/context.py:200-201,231,317）。Playwright 的 "response"
        # 事件发在 BrowserContext 上，因此挂到该 Playwright context 即可。
        try:
            if not getattr(browser, "_ae_listener_on", False):
                seen = set()

                async def on_response(resp):
                    try:
                        # 调试：每次响应都写文件，确认监听器是否真的在触发、以及 URL/类型
                        try:
                            with open("/tmp/ae_capture.log", "a") as _f:
                                _f.write(
                                    f"{resp.request.method} {resp.status} "
                                    f"{resp.url[:220]} ctype={resp.headers.get('content-type','')}\n"
                                )
                        except Exception:
                            pass
                        ctype = (resp.headers.get("content-type", "") or "").lower()
                        url = resp.url
                        method = resp.request.method
                        status = resp.status
                        is_json = ("json" in ctype) or ("javascript" in ctype)
                        body = ""
                        if is_json:  # 仅对 json/javascript 读取并保存响应体，避免内存膨胀
                            try:
                                body = (await resp.body() or b"").decode("utf-8", "replace")
                            except Exception:
                                body = ""
                        captured["responses"].append(
                            {
                                "method": method,
                                "url": url,
                                "status": status,
                                "content_type": resp.headers.get("content-type", ""),
                                "body": body,
                            }
                        )
                        # 限长，防止 SPA 海量静态资源撑爆内存
                        if len(captured["responses"]) > 400:
                            captured["responses"] = captured["responses"][-400:]
                    except Exception as _e:  # 不再静默吞掉：把异常写进调试日志便于定位
                        try:
                            with open("/tmp/ae_capture.log", "a") as _f:
                                _f.write(f"[on_response ERROR] {type(_e).__name__}: {_e}\n")
                        except Exception:
                            pass

                # 多版本兼容地取得 Playwright BrowserContext（必须有 .on 才是真对象）
                target = None
                sess = getattr(browser, "session", None)
                if sess is not None and hasattr(getattr(sess, "context", None), "on"):
                    target = sess.context
                if target is None and hasattr(browser, "on"):
                    target = browser
                if target is None and hasattr(getattr(browser, "context", None), "on"):
                    target = browser.context
                if target is None:
                    raise RuntimeError(
                        "无法从注入对象取得 Playwright BrowserContext（均无 .on）"
                    )
                target.on("response", on_response)
                # 兜底：browser-use 若后续新建 context，也挂上监听（防导航换 context）
                try:
                    pw_browser = getattr(target, "browser", None)
                    if pw_browser is not None and hasattr(pw_browser, "on"):
                        pw_browser.on(
                            "context",
                            lambda new_ctx: new_ctx.on("response", on_response)
                            if hasattr(new_ctx, "on") else None,
                        )
                except Exception:
                    pass
                browser._ae_listener_on = True
        except Exception as e:  # best-effort，不影响核心
            import traceback as _tb
            captured["capture_error"] = f"{e}\n{_tb.format_exc()}"
            return f"监听挂载失败：{e}"
        return "已挂载响应监听（context 级，捕获 application/json 与 javascript 响应）"

    @controller.action("列出已捕获的网络响应（用于定位岗位列表 XHR 接口并填写 record_recipe）")
    def list_captured() -> str:
        """把已捕获的响应回传给 LLM，使其能据此填写 record_recipe。

        关键修复：capture_requests 只挂载监听、不回传内容，导致 LLM 看不到
        任何接口信息、无法调用 record_recipe。本动作把响应（JSON 截断预览）回传。
        """
        if captured.get("capture_error"):
            return f"捕获异常：{captured['capture_error']}。请重新调用 capture_requests 挂载监听。"
        resps = captured.get("responses", [])
        if not resps:
            return ("尚未捕获到任何响应（监听器已挂载）。可能原因："
                    "① 搜索未触发新请求（数据来自缓存/Service Worker）；"
                    "② 监听挂载在了错误的 context 上。建议重新挂载监听后再搜索。")
        # 内容类型分布，用于判断到底有没有捕获到、都是哪些类型
        from collections import Counter
        types = Counter((r.get("content_type") or "?").split(";")[0] or "?" for r in resps)
        hist = ", ".join(f"{t}:{n}" for t, n in types.most_common(12))
        out = [f"共捕获 {len(resps)} 条响应，内容类型分布：{hist}"]
        jsons = [r for r in resps[-30:] if r.get("body")]
        if not jsons:
            out.append("（尚无 json/javascript 响应体；若分布里没有 application/json，"
                       "说明岗位接口可能用了其他类型或来自缓存，请重新挂载监听后触发搜索）")
        for r in jsons:
            body = r["body"]
            try:
                obj = json.loads(body)
                body = json.dumps(obj, ensure_ascii=False)[:800]
            except Exception:
                body = body[:800]
            out.append(f"[{r.get('method')}] {r.get('url')} (status={r.get('status')})\n  {body}")
        text = "\n".join(out)
        # 调试：把 list_captured 实际返回写文件，确认是否正确回传了接口信息
        try:
            with open("/tmp/ae_listcaptured.log", "w") as _f:
                _f.write(f"count={len(resps)} json={len(jsons)}\n{text}\n")
        except Exception:
            pass
        return text

    return controller, captured


def run_full_explore(site: str, base_url: str, keyword: str) -> tuple[RecipeCandidate, dict]:
    """用 browser-use 全自主探索一个站点，返回 RecipeCandidate + 观测。

    让 browser-use 处理内层多步（打开根域名→搜索关键词→定位列表接口），
    外层 LangGraph 负责 guardrail/critic/memory 与验证。
    """
    import asyncio

    from browser_use import Agent

    from app.llm import get_llm

    browser = _ensure_browser(site)
    controller, captured = _build_controller()
    task = (
        f"打开校招站点根域名 {base_url}（不要带深层链接）。"
        f"第一步：先调用 capture_requests 挂载网络监听（必须在搜索之前）。"
        f"第二步：找到岗位搜索入口，搜索关键词「{keyword or '算法'}」，触发岗位列表加载。"
        f"第三步：调用 list_captured 查看已捕获到的网络响应，找到返回岗位列表的那个 XHR JSON 接口"
        f"（观察其 URL、请求方法、以及响应体里的字段结构）。"
        f"第四步：调用 record_recipe 记录该接口结构："
        f"list_api（接口URL）、method、id_field（岗位唯一标识字段名）、"
        f"title_field（岗位标题字段名）、list_path（岗位数组在响应JSON中的路径，如 data.list）、"
        f"keyword_param（关键词参数名，若在请求体里则写 keyword_in_body=true 并填 request_body）。"
        f"注意：list_captured 看到的响应体就是真实样本，请据此准确填写字段名与 list_path，不要臆测。"
    )
    agent = Agent(
        task=task,
        llm=get_llm(),
        browser=browser,
        controller=controller,
        use_vision=False,  # 关闭视觉输入，减少 token 消耗并避免多模态依赖
        enable_memory=False,  # 关闭 browser-use 自带 mem0 记忆（需 OPENAI_API_KEY），记忆由我们的角色层管理
    )

    async def _run_and_close():
        # 必须在 agent.run 所在的（存活的）事件循环内关闭浏览器：
        # asyncio.run 返回后会关闭该临时 loop，若在外面关会触发
        # 「Event loop is closed」。在 loop 内关还能释放 user_data_dir 锁，
        # 否则残留浏览器进程会锁住 profile 目录，导致下一轮新浏览器也起不来。
        try:
            return await agent.run()
        finally:
            try:
                closer = browser.close()
                if hasattr(closer, "__await__"):
                    await closer
            except Exception:
                pass

    try:
        result = asyncio.run(_run_and_close())
    finally:
        # 作废单例浏览器：asyncio.run 已关闭临时事件循环，本 Browser 句柄随之失效。
        # 不置空的话，下一轮 _ensure_browser 会复用这个「Event loop is closed」的死句柄，
        # 导致每轮都报「Failed to create new browser session」并被 browser-use 无限重试
        # （每个重试都消耗一次 LLM 调用），表现为一直卡在 Step 1 空转烧 token。
        global _BROWSER
        with _BROWSER_LOCK:
            _BROWSER = None
    recipe_dict = captured.get("recipe")
    if not recipe_dict:
        # 把 browser-use 的最终输出作为诊断信息抛出，避免盲 500
        raise RuntimeError(
            "browser-use 未回传 record_recipe（探索未成功定位接口）。"
            f" agent 最终输出：\n{str(result)[:2000]}"
        )
    cand = RecipeCandidate(**{k: v for k, v in recipe_dict.items()
                              if k in RecipeCandidate.model_fields})
    return cand, {"responses": captured.get("responses", []),
                   "requests": captured.get("requests", []),
                   "recipe": recipe_dict}
