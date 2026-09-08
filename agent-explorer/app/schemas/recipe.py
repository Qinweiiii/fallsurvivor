"""请求/响应与 RecipeCandidate schema。

`RecipeCandidate` 的字段逐字段对齐 Go 侧
`backend/internal/ai/explorer.go` 的 `RecipeCandidate`，保证 HTTP 契约一致
（建议后续抽出 shared `schemas/recipe.json`，Go 用 jsonschema 校验入参）。
"""
from typing import Dict, List, Optional

from pydantic import BaseModel, Field


class RecipeCandidate(BaseModel):
    # 列表接口 URL（含查询参数模板）
    list_api: str
    # 详情接口 URL 模板，{id} 占位岗位 ID
    detail_api: str = ""
    # 详情页 URL 模板，{id} 占位
    detail_url_template: str = ""
    # 列表接口 HTTP 方法
    method: str
    # POST 请求体原文（由模型从真实请求推导，不是代码猜测）
    request_body: str = ""
    request_content_type: str = ""
    request_headers: Dict[str, str] = Field(default_factory=dict)
    # 响应中岗位 ID / 标题字段名
    id_field: str
    title_field: str
    # 岗位数组在响应 JSON 中的路径，如 data.positionList
    list_path: str
    # 关键词对应的查询参数名
    keyword_param: str = ""
    # 关键词是否写进请求体（POST 型搜索常见）
    keyword_in_body: bool = False
    # 语义字段 -> 响应字段路径映射
    field_map: Dict[str, str] = Field(default_factory=dict)
    notes: str = ""
    confidence: int = 0


class TraceStep(BaseModel):
    step: int
    role: str  # guardrail | planner | actor | critic | memory
    action: str = ""
    target: str = ""
    reasoning: str = ""
    result: str = ""
    confidence: int = 0


class ExploreRequest(BaseModel):
    site: str
    base_url: str
    keyword: str = ""
    save: bool = True


class ExploreResponse(BaseModel):
    status: str  # success | failed
    recipe: Optional[RecipeCandidate] = None
    trace: List[TraceStep] = Field(default_factory=list)
    reason: str = ""
    # verify 自修正闭环结论（对齐 Go verifyAndRefine）
    verified: bool = False
    verify_result: Optional[Dict] = None
    refine_rounds: int = 0
