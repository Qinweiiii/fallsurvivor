/**
 * Worker 与后端之间的数据契约。
 * 字段命名与 Go 侧的 DTO 保持一致。
 */

/** 表单字段元信息。ref 是 Worker 生成的不透明句柄。 */
export interface FormField {
  ref: string;
  name: string;
  label: string;
  placeholder: string;
  type: string;
  required: boolean;
  options?: string[];
  max_length?: number;
}

/** 单个填写项。 */
export interface FillItem {
  ref: string;
  value: string;
}

export interface OpenSessionRequest {
  task_id: string;
  site_key: string;
  url: string;
}

export interface OpenSessionResponse {
  task_id: string;
  logged_in: boolean;
  needs_login: boolean;
  current_url: string;
  step: string;
  message: string;
}

export interface ExtractResponse {
  task_id: string;
  current_url: string;
  fields: FormField[];
  blocked: boolean;
  message: string;
}

export interface FillRequest {
  task_id: string;
  items: FillItem[];
}

export interface FillResponse {
  task_id: string;
  filled: number;
  failed: number;
  failed_refs: string[];
  current_url: string;
  message: string;
}

export interface StatusResponse {
  task_id: string;
  alive: boolean;
  current_url: string;
  step: string;
}

/**
 * 抓取 JD 页面正文的请求与响应。
 * 仅读取已登录浏览器当前可见页面的文本，不点击、不导航、不执行任意 JS。
 */
export interface ScrapeRequest {
  task_id: string;
}

export interface ScrapeResponse {
  task_id: string;
  needs_login: boolean;
  current_url: string;
  page_text: string;
  title: string;
  message: string;
}

/**
 * 导航动作：由后端 AI 决策，Worker 按类型安全执行。
 * 所有动作都受安全约束限制（见 nav_act.ts）：
 *   - 不执行任意 JS；
 *   - click_ref/input_ref/search 优先使用当前探索快照元素；
 *   - navigate 仅允许 http/https 公网地址。
 */
export type NavActionType = 'click' | 'click_ref' | 'input_ref' | 'scroll' | 'navigate' | 'wait' | 'search' | 'back';

export interface NavAction {
  type: NavActionType;
  /**
   * click_ref/input_ref/search：来自最近一次 /explore/snapshot 的元素 ref。
   * search 带 ref 时必须作用于该元素，不允许重新扫描页面猜测输入框。
   */
  ref?: string;
  /** click：目标元素可见文案（归一化后子串匹配）。 */
  text?: string;
  /** scroll：方向，默认 down。 */
  direction?: 'down' | 'up';
  /** scroll：像素数，默认 600，限幅 100~5000。 */
  amount?: number;
  /** navigate：目标 URL（需通过安全校验）。 */
  url?: string;
  /** wait：秒数，默认 1，限幅 0~10。 */
  seconds?: number;
  /** search：输入到 ref 指定搜索框的关键词。未带 ref 的旧 Recipe 才允许 Worker 回退定位。 */
  keyword?: string;
}

export interface NavActRequest {
  task_id: string;
  action: NavAction;
}

export interface NavActResponse {
  task_id: string;
  ok: boolean;
  current_url: string;
  message: string;
  error?: string;
}

/**
 * 从列表页抽取的结构化岗位卡片。
 *
 * 仅基于 DOM 中真实存在的岗位详情链接与可见文本，
 * 不依赖任何从后端下发的任意选择器（选择器固定写在站点适配器内）。
 */
export interface JobCard {
  /** 岗位详情页绝对 URL。 */
  url: string;
  /** 岗位标题（来自详情链接可见文本）。 */
  title: string;
  /** 卡片容器可见文本（用于回退时补充地点 / 部门等线索）。 */
  raw_text?: string;
  /**
   * 站点侧提供的权威结构化部门信息，格式 "部门名 - 事业群"，如 "腾讯金融科技 - CDG"。
   * 后端 pipeline 会优先采用此字段作为 job.department，比模型从长文本解析更可靠。
   * 一个岗位可能对应多个部门，adapter 会按部门展开成多条独立 JobCard。
   */
  department?: string;
}

/** POST /page/extract-jobs 请求。 */
export interface ExtractJobsRequest {
  task_id: string;
}

/** POST /page/extract-jobs 响应。 */
export interface ExtractJobsResponse {
  task_id: string;
  current_url: string;
  jobs: JobCard[];
  count: number;
  message: string;
  error?: string;
}

// ---------------- Exploration（站点探索） ----------------

/**
 * 观测到的网络请求（脱敏 + 截断后）。
 * 字段与 common/network_observer.ts 的 ObservedRequest 对应。
 */
export interface NetworkRecord {
  seq: number;
  method: string;
  url: string;
  status: number;
  content_type: string;
  size: number;
  sample: string;
  full_sample?: string;
  schema_summary?: string;
  /** 请求体片段（已脱敏截断）。POST 型接口的可复现关键。 */
  request_body?: string;
  /** 请求的 Content-Type，决定复现时该用 JSON 还是 form。 */
  request_content_type?: string;
  /** 请求头白名单快照（不含任何凭证类头部）。 */
  request_headers?: Record<string, string>;
  at: number;
}

/** 打分后的网络候选。 */
export interface RankedNetworkRecord {
  record: NetworkRecord;
  score: number;
  reasons: string[];
}

/** POST /explore/observe-start 请求。 */
export interface ObserveStartRequest {
  task_id: string;
}

/** POST /explore/observe-start 响应。 */
export interface ObserveStartResponse {
  task_id: string;
  /** 当前观测游标，作为后续 Diff 的基线。 */
  cursor: number;
  current_url: string;
}

/** POST /explore/observe-diff 请求。 */
export interface ObserveDiffRequest {
  task_id: string;
  /** 只返回 seq 大于该值的请求。 */
  since?: number;
  /** 返回条数上限。 */
  limit?: number;
  /** 是否只返回经过规则打分筛选的候选。 */
  ranked_only?: boolean;
  /** 打分筛选的返回条数。 */
  top_n?: number;
}

/** POST /explore/observe-diff 响应。 */
export interface ObserveDiffResponse {
  task_id: string;
  cursor: number;
  current_url: string;
  /** 新增的全部请求。 */
  new_requests: NetworkRecord[];
  /** 经过规则打分排序的候选（LLM 应优先关注这些）。 */
  candidates: RankedNetworkRecord[];
}

/** POST /explore/inspect-request 请求。 */
export interface InspectRequestRequest {
  task_id: string;
  seq: number;
}

/** POST /explore/inspect-request 响应。 */
export interface InspectRequestResponse {
  task_id: string;
  current_url: string;
  record: NetworkRecord | null;
  error?: string;
}

/** POST /explore/snapshot 请求：读取当前页面的可交互元素。 */
export interface SnapshotRequest {
  task_id: string;
}

/** POST /page/markdown 请求：读取渲染后正文的 Markdown。 */
export interface MarkdownRequest {
  task_id: string;
}

/**
 * POST /page/markdown 响应。
 *
 * 用于「读渲染结果」而非「逆向接口」的采集路径：
 * 重度 SPA 站点的列表接口常带鉴权或上下文参数，脱离浏览器无法复现，
 * 但页面本身往往无需登录就渲染出了全部岗位。
 */
export interface MarkdownResponse {
  task_id: string;
  current_url: string;
  title: string;
  /** 渲染后正文的 Markdown（已脱敏截断）。 */
  markdown: string;
  /** 是否因超长被截断。截断时调用方可考虑翻页或缩小范围。 */
  truncated: boolean;
  error?: string;
}

/** 页面上的一个可交互元素。 */
export interface SnapshotElement {
  /** Worker 生成的不透明句柄，供 click 使用。 */
  ref: string;
  tag: string;
  text: string;
  placeholder: string;
  aria_label: string;
  /** 元素类型（如 text / checkbox / radio / submit）。 */
  input_type: string;
  /** 链接元素的绝对地址；为空表示该元素没有可复用链接。 */
  href?: string;
  visible: boolean;
}

/** POST /explore/snapshot 响应。 */
export interface SnapshotResponse {
  task_id: string;
  current_url: string;
  title: string;
  /** 可交互元素（已过滤不可见项，条数有上限）。 */
  elements: SnapshotElement[];
  /** 页面正文片段，供 LLM 理解当前所处阶段。 */
  text_sample: string;
  /** 疑似岗位卡片的容器文本（启发式，可能为空）。 */
  card_samples: string[];
}
