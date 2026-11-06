# LogPulse：日志智能分析器

一个开箱即用的日志智能分析平台：导入任意格式的日志 → 自动解析 → 聚类出日志模式 → 自动检测异常并给出根因线索。

| 层 | 技术栈 |
| --- | --- |
| 后端 | Go 1.25 · 标准库 `net/http`（Go 1.22+ 路由）· [pgx v5](https://github.com/jackc/pgx) · [Anthropic Go SDK](https://github.com/anthropics/anthropic-sdk-go) |
| 前端 | React 19 · TypeScript · Vite 6 · Tailwind CSS 4 · TanStack Query 5 · React Router 7 · Recharts 3 |
| 数据库 | PostgreSQL 16（全文检索 `tsvector` + GIN 索引、JSONB 属性） |
| 部署 | Docker Compose（Postgres + 后端 + Nginx 托管前端） |

## 功能

- **多格式自动解析**：JSON 行日志、Nginx/Apache combined 访问日志、RFC3164 Syslog、`时间戳 级别 [服务] 消息` 类文本日志，无法识别的行按纯文本处理并根据关键词推断级别。
- **日志模式聚类**：把消息中的可变部分（数字、IP、UUID、十六进制 ID、URL、时间、路径参数、引号字符串等）替换为占位符，得到模板；相同 `(服务, 级别, 模板)` 归为一类。几十万行日志通常只有几十种模式。
  - 例：`payment gateway timeout after 3333ms for order 73554` → `payment gateway timeout after <NUM> for order <NUM>`
- **异常检测**（后台每分钟一次，导入后也会立即触发）：
  - **错误突增 / 流量突增**：按服务、按时间桶统计，用滑动基线上的**鲁棒 z-score（中位数 + MAD）** 判断，历史中的尖峰不会抬高基线；相邻的突增桶会合并为一个事件，避免告警风暴。
  - **新错误模式**：系统积累足够历史后，首次出现的 WARN/ERROR/FATAL 模式会被标记。
  - **根因线索**：每个突增事件附带窗口内占比最高的日志模式，一眼看出是哪类错误导致的。
- **AI 异常分析**（可选）：调用 Claude 为异常生成一句话结论、影响评估、带日志证据的可能原因和按顺序排列的排查步骤。详见下文。
- **检索**：PostgreSQL 全文检索（支持 `websearch` 语法，如 `timeout -health`、`"connection refused"`）+ 子串匹配兜底，可按级别、服务、模式筛选。
- **可视化看板**：日志量趋势（按级别堆叠）、服务分布、级别分布、最新异常、高频错误模式；支持深色模式与移动端。

## 快速开始

### 方式一：Docker Compose（推荐）

```bash
docker compose up -d --build
make sample        # 生成 2 小时的演示日志（含一次注入的故障）并导入
```

打开 http://localhost:3000 。后端 API 位于 http://localhost:8080 。

### 方式二：本地开发

```bash
make db            # 只启动 PostgreSQL
make backend       # 终端 1：go run ./cmd/server（:8080，启动时自动迁移数据库）
make frontend      # 终端 2：Vite 开发服务器 http://localhost:5173（/api 自动代理到 :8080）
make sample        # 导入演示数据
```

运行测试：`make test`

## AI 异常分析

设置 `ANTHROPIC_API_KEY` 后启用（未设置时其余功能不受影响，页面会提示如何开启）：

```bash
export ANTHROPIC_API_KEY=sk-ant-...
docker compose up -d --build
```

- **自动生成**：每轮检测后，为未确认的中/高危异常自动生成分析，高危优先；每轮最多 `LLM_AUTO_PER_RUN` 个，以控制成本。
- **手动生成**：异常卡片上的「AI 分析 / 重新分析」按钮，或 `POST /api/anomalies/{id}/insight`。
- **发送给模型的上下文**：异常指标、窗口内 Top 模式、按模式分散抽样的窗口内日志（默认最多 30 行）、异常开始前 10 分钟的告警/错误日志、该服务的常见基线模式。
- **结构化输出**：使用 Structured Outputs，模型按固定 JSON 结构返回 `summary`、`impact`、`likely_causes`（含 `evidence`）、`next_steps`、`confidence`，无需解析自由文本。
- **安全与隐私**：
  - 日志会发送到 Anthropic API。发送前会对 `password=`、`token=`、`api_key=`、`Authorization:`、Bearer 令牌和邮箱地址做脱敏；如有更严格的合规要求，请在 `internal/llm/llm.go` 的 `redactRules` 中补充规则。
  - 系统提示词明确要求把日志内容当作数据而非指令，降低日志中被注入恶意指令的风险。
  - 启用了服务端备用模型（`fallbacks: "default"`）：请求若被模型的安全策略拒绝，会自动改用推荐的备用模型重试。
- **失败处理**：限流、5xx、网络错误等临时性错误不记录，下一轮自动重试；认证失败、被拒绝等错误会记录在 `ai_error` 中并显示在页面上，不再自动重试（可手动重新分析）。

## 导入日志

```bash
# 1. 上传文件（multipart），service 可选，默认取文件名
curl -F file=@app.log -F service=api http://localhost:8080/api/logs/upload

# 2. 推送文本，一行一条，任意支持的格式
tail -n 1000 /var/log/nginx/access.log | \
  curl -X POST 'http://localhost:8080/api/logs?service=nginx' --data-binary @-

# 3. 推送 JSON（数组 / {"logs": [...]} / 单个对象）
curl -X POST http://localhost:8080/api/logs -H 'Content-Type: application/json' \
  -d '[{"level":"error","service":"api","msg":"db timeout","user_id":42}]'
```

JSON 字段会自动识别：时间（`timestamp`/`time`/`ts`/`@timestamp`，支持 RFC3339 与秒/毫秒/微秒/纳秒时间戳）、级别（`level`/`severity`/…）、消息（`message`/`msg`/…）、服务（`service`/`app`/`logger`/…）、主机（`host`/`hostname`/`pod`/…），其余字段存入 `attributes`（JSONB）。

## API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/health` | 健康检查 |
| POST | `/api/logs` | 推送日志（JSON 或按行文本），`?service=` 默认服务名 |
| POST | `/api/logs/upload` | 上传日志文件（字段 `file`、`service`） |
| GET | `/api/logs` | 检索：`q` `level`（逗号分隔）`service` `host` `pattern_id` `from` `to`（RFC3339）`limit` `offset` |
| GET | `/api/stats/overview` | 总量、错误率、级别分布、Top 服务（支持 `from`/`to`） |
| GET | `/api/stats/timeseries` | 时间序列：`bucket`（如 `1m`、`1h`）及与检索相同的筛选参数 |
| GET | `/api/services` | 服务列表 |
| GET | `/api/patterns` | 日志模式：`service` `level` `q` `sort`（`count`/`recent`/`new`）`limit` |
| GET | `/api/anomalies` | 异常列表：`kind` `severity` `service` `acknowledged` `limit` |
| POST | `/api/anomalies/{id}/ack` | 确认/重新打开异常，body：`{"acknowledged": true}` |
| POST | `/api/anomalies/{id}/insight` | 为异常生成（或重新生成）AI 分析 |
| GET | `/api/features` | 可用功能，如 `{"ai_insight": true, "ai_model": "..."}` |
| POST | `/api/analyze` | 立即执行一次异常检测 |

## 配置（环境变量）

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ADDR` | `:8080` | 监听地址 |
| `DATABASE_URL` | `postgres://loganalyzer:loganalyzer@localhost:5432/loganalyzer?sslmode=disable` | 数据库连接串 |
| `ALLOWED_ORIGIN` | `*` | CORS 允许来源 |
| `MAX_UPLOAD_MB` | `50` | 单次上传大小上限 |
| `STATIC_DIR` | 空 | 设置后后端同时托管前端构建产物（单二进制部署） |
| `ANALYZE_INTERVAL` | `1m` | 后台检测周期，`0` 表示仅在导入后触发 |
| `ANALYZE_BUCKET` | `1m` | 时间桶大小 |
| `ANALYZE_BASELINE_BUCKETS` | `30` | 基线窗口桶数 |
| `ANALYZE_Z_THRESHOLD` | `3` | z-score 阈值（≥2 倍为高危，≥1.5 倍为中危） |
| `ANALYZE_MIN_COUNT` | `5` | 单桶计数低于该值不告警，避免低流量噪声 |
| `ANTHROPIC_API_KEY` | 空 | 设置后启用 AI 异常分析 |
| `LLM_MODEL` | `claude-opus-5-5` | 使用的 Claude 模型 |
| `LLM_EFFORT` | `medium` | 推理强度：`low` / `medium` / `high` / `xhigh` / `max`，越高越细致但更慢、更贵 |
| `LLM_AUTO_SUMMARIZE` | `true` | 是否自动为新的中/高危异常生成分析 |
| `LLM_AUTO_PER_RUN` | `3` | 每轮检测最多自动生成的分析数 |
| `LLM_SAMPLE_LINES` | `30` | 每个异常附带的样例日志行数上限 |
| `LLM_ENABLED` | `true` | 设为 `false` 可在保留 API Key 的情况下关闭 AI 分析 |
| `ANALYZE_LOOKBACK` | `24h` | 每次分析回溯范围（以最新一条日志的时间为终点，因此导入的历史日志也能分析） |

## 项目结构

```
backend/
  cmd/server/            服务入口
  cmd/loggen/            演示日志生成器（混合格式 + 注入故障）
  internal/parser/       多格式日志解析
  internal/pattern/      模板提取与聚类
  internal/analyzer/     异常检测算法（纯函数，有单测）与调度
  internal/llm/          AI 异常分析（上下文抽样、脱敏、Claude 调用、结构化输出）
  internal/store/        PostgreSQL 读写（批量 COPY 写入、模式 upsert）
  internal/db/           连接与内嵌 SQL 迁移
  internal/api/          REST 接口
frontend/
  src/pages/             总览 / 日志检索 / 日志模式 / 异常检测 / 导入日志
  src/components/        布局与通用 UI
  src/lib/               API 客户端与格式化工具
docker-compose.yml
```

## 数据模型

- `log_patterns`：模式（模板、样例、计数、首次/最近出现时间），`hash` 唯一。
- `logs`：日志明细，`pattern_id` 关联模式；`search` 为生成列 `tsvector`，配合 GIN 索引做全文检索；`attributes` 为 JSONB。
- `anomalies`：异常事件，`dedup_key` 保证重复分析幂等；`ai_insight`（JSONB）、`ai_model`、`ai_error`、`ai_generated_at` 保存 AI 分析结果。

## 后续可扩展方向

- 告警通知（Webhook / 邮件 / IM）
- 按时间分区（`logs` 表按天分区）与数据保留策略，支撑更大数据量
- Agent 采集端（如对接 Fluent Bit / Vector 的 HTTP 输出）
