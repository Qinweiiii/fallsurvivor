# 秋招 OS（Job Hunt OS）

一个服务于个人秋招过程的轻量级求职管理平台。

它帮你**更快发现适合的岗位、整理准备投递的岗位、辅助完成重复的申请表填写、清晰记录整个投递进度**。

它**不是**无人值守的自动投递机器人。

---

## 核心边界

这几条是产品的硬性约束，写在代码里，也写在这里：

| 边界 | 落地方式 |
|---|---|
| 系统不点击最终提交按钮 | 状态机中不存在自动提交路径；API 只有 `mark-submitted`（用户确认已提交）；Worker 只滚动高亮提交按钮，不点击 |
| 敏感信息不填写、不存储 | `internal/security/sensitive.go` 是唯一黑名单来源；数据库有 `NOT (is_sensitive AND is_filled)` 强约束；Worker 侧再校验一次 |
| 不绕过验证码与风控 | 检测到验证码/风控即停止并交回用户，任务标记为 `BLOCKED` |
| 登录态只留本地 | 保存在 `worker-browser/.auth/`（权限 0700），不入库、不进日志、不发给 AI |
| 密钥不接触前端 | 所有 Key 只在后端环境变量中读取，前端只有 `NEXT_PUBLIC_API_BASE_URL` |

---

## 技术栈

| 层 | 技术 |
|---|---|
| 前端 | Next.js 14 · React 18 · TypeScript · Tailwind CSS |
| 后端 | Go · Gin · GORM |
| 存储 | PostgreSQL · Redis |
| 异步 | asynq（Redis 任务队列） |
| AI | DeepSeek API |
| 搜索 | Tavily API |
| 浏览器 | Playwright（Node + TypeScript，独立进程） |
| 部署 | Docker Compose |

---

## 目录结构

```text
fallsurvivor/
├── docs/                       # 6 份设计文档
├── backend/                    # Go 单体后端
│   ├── cmd/{server,worker,migrate}
│   ├── config/                 # 环境变量与城市评分
│   ├── internal/
│   │   ├── handler/            # HTTP 层（只做参数校验与转发）
│   │   ├── service/            # 业务逻辑
│   │   │   ├── search/         # 搜索管线：标准化 / 去重 / 评分
│   │   │   ├── application/    # 投递状态机
│   │   │   ├── job/ profile/
│   │   ├── repository/         # 唯一访问数据库的层
│   │   ├── model/              # 数据模型与状态常量
│   │   ├── ai/                 # 所有 DeepSeek 调用收口于此
│   │   ├── source/             # JobSource 抽象：Official / BOSS / Tavily
│   │   ├── browser/            # 与 Playwright Worker 协作
│   │   ├── security/           # 敏感字段黑名单 + 脱敏
│   │   └── task/               # 异步任务定义
│   ├── pkg/
│   │   ├── safefetch/          # 带 SSRF 防护的抓取客户端
│   │   └── {response,pagination,logger}/
│   └── migrations/             # SQL 迁移（13 表 + 索引 + CHECK 约束）
├── worker-browser/             # Playwright Worker
│   └── src/
│       ├── common/             # 字段提取 / 填写 / 敏感守卫 / 浏览器管理
│       └── sites/              # 各招聘站适配器
└── frontend/                   # Next.js
    └── src/
        ├── app/                # 6 个页面
        ├── components/         # 表格 / 筛选 / Drawer / UI
        └── lib/                # API 客户端与类型
```

---

## 快速开始

### 1. 准备环境变量

```bash
cp .env.example .env
```

编辑 `.env`，至少填写：

```env
POSTGRES_PASSWORD=你自己的强密码
DATABASE_URL=postgres://jobos:你自己的强密码@localhost:5432/jobos?sslmode=disable
REDIS_URL=redis://localhost:6379/0

# 可选：不填则降级为纯规则评分 + 无法在线获取岗位
DEEPSEEK_API_KEY=
TAVILY_API_KEY=

# 建议生成一个随机长串
BROWSER_WORKER_TOKEN=
```

生成 Worker token：

```bash
openssl rand -hex 32
```

### 2. 启动依赖并初始化数据库

```bash
make infra      # 启动 PostgreSQL + Redis
make migrate    # 建表
make seed       # 写入默认用户与求职画像
```

### 3. 启动服务（四个终端）

```bash
make server     # 后端 API      → http://127.0.0.1:9090
make worker     # 异步任务消费者
make fe         # 前端           → http://localhost:3000
make bw         # Playwright Worker（需要时再启动）
```

首次使用 Worker 前需安装 Chromium：

```bash
make bw-install
```

### 4. 或者用 Docker Compose

```bash
make up
```

> Playwright Worker 刻意不进容器：它需要打开你能看见的浏览器窗口，
> 由你手动登录、处理验证码、点击最终提交。

---

## 使用流程

```text
求职画像（设置方向 / 语言优先级 / 城市顺序）
   ↓
岗位总览 → 点击「获取岗位」
   ↓
筛选查看 → 打开详情 Drawer 看完整 JD 与匹配分析
   ↓
勾选岗位 → 加入岗位车
   ↓
岗位车 → 批量「开始准备投递」
   ↓
投递进度 → 「开始辅助填写」
   ↓
浏览器打开 → 你登录（仅第一次）
   ↓
AI 自动填写普通字段，敏感字段留空
   ↓
「有 3 项需要你手动填写」→ 你填好 → 点击继续
   ↓
你自己检查 → 你自己点击招聘网站的提交按钮
   ↓
回到系统 → 点击「我已在网站完成提交」
   ↓
跟踪笔试 / 一面 / 二面 / HR 面 / Offer
```

---

## 设计要点

### 确定性逻辑与 LLM 的分工

明确区分，避免把可靠性交给模型：

| 交给代码 | 交给 DeepSeek |
|---|---|
| URL 规范化与去重 | 检索式生成 |
| 字段标准化 | JD 结构化抽取 |
| 分页、排序、数据库筛选 | 岗位语义匹配 |
| 状态机与时间计算 | 表单字段语义映射 |
| 规则评分权重 | 简历结构化 |

**LLM 不可用时系统仍然可用**：检索式退化为模板、评分退化为纯规则、字段映射退化为规则表。

### 四层岗位去重

1. `official_url` 精确相同
2. `normalized_url` 相同（剔除追踪参数、统一 host、排序 query）
3. `company + title + location` 归一化指纹
4. 语义相似度 —— 第一版**不启用**，避免误合并不同部门的同名岗位

同一岗位来自多个来源时只建一条 `job`，其余记入 `job_sources`，并保留优先级最高的来源（官方 > BOSS > 全网）作为主记录。

### 匹配度计算

```text
最终分 = 0.4 × 规则分 + 0.6 × LLM 分
```

规则分五个维度（合计 100）：岗位方向 30、技术语言 25、城市 20、毕业届次 15、公司偏好 10。

几个刻意的设计取舍：
- 岗位只要求 Java 而你偏好 Go → **显著降分但不低于 30**，因为你仍可能愿意投
- 岗位未标注届次 → **给中间分而非归零**，避免误杀信息不全的岗位
- 公司不在偏好列表 → **给一半分**，你并没有说只投大厂

---

## 常见问题

**没配 API Key 能用吗？**
能。岗位总览、岗位车、投递进度、状态跟踪全部可用，只是无法在线获取新岗位、匹配度只有规则分。

**为什么简历要我手动粘贴文本？**
PDF/DOCX 的文本抽取需要引入重型依赖且解析质量不稳定。第一版用你粘贴文本换取更稳定的解析效果和更低的维护成本。

**为什么后端只监听 127.0.0.1？**
这是个人本地工具，没有认证体系，不应暴露到局域网。容器内通过 `BIND_ALL=true` 放开。

**Worker 会不会帮我点提交？**
不会，而且不是"配置成不点"，是代码里没有这条路径。你可以搜一下 `click`，Worker 里只有「进入表单页」的入口按钮会被点击，提交按钮只做滚动和高亮。

---

## 开发命令

```bash
make help           # 查看全部命令
make test           # 运行后端单测
make vet            # Go 静态检查
make migrate-down   # 回滚最后一个迁移
```

后端单测重点覆盖了几处关键逻辑：
- 状态机不存在自动提交路径
- 敏感字段一律跳过且不携带值
- SSRF 防护拒绝内网地址与非标端口
- 去重指纹对同一岗位的不同写法保持一致
- 语言不匹配时降分但不判定为不可投

---

## 待办

- [ ] 面试日程管理（`interviews` 表已就绪）
- [ ] JD 更新检测
- [ ] 求职数据统计图表
- [ ] 简历针对性优化建议
- [ ] 更多公司官方站适配器（`internal/source/official.go` 中扩展 `CompanyAdapter`）
