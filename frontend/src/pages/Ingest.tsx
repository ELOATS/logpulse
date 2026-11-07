import { useRef, useState, type DragEvent } from 'react'
import { Link } from 'react-router'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { CheckCircle2, FileText, FileUp, X } from 'lucide-react'
import clsx from 'clsx'
import { api, type IngestResult } from '../lib/api'
import { fmtNumber } from '../lib/format'
import { Card, CopyButton, ErrorBox, PageHeader, Segmented, buttonClass, codeBlockClass, ghostButtonClass, inputClass } from '../components/ui'

const FORMATS = ['JSON 行', 'logfmt（slog / logrus）', 'Go log', 'zap', 'Nginx / Apache', 'Syslog', '通用文本', 'Go panic 堆栈', 'JS / Java 堆栈']

const EXAMPLE = `{"ts":"2024-05-01T10:00:00Z","level":"error","service":"api","msg":"db timeout after 3000ms"}
time=2024-05-01T10:00:00Z level=ERROR msg="query failed" err="context deadline exceeded"
2024-05-01 10:00:01 WARN [order-service] stock low for sku 991
127.0.0.1 - - [01/May/2024:10:00:02 +0000] "GET /api/users/42 HTTP/1.1" 500 512 "-" "curl/8.0"
panic: runtime error: invalid memory address or nil pointer dereference

goroutine 1 [running]:
main.handler(...)
	/app/main.go:42 +0x1d`

function formatSize(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

export default function Ingest() {
  const qc = useQueryClient()
  const fileRef = useRef<HTMLInputElement>(null)
  const [file, setFile] = useState<File | null>(null)
  const [service, setService] = useState('')
  const [text, setText] = useState('')
  const [dragging, setDragging] = useState(false)
  const [result, setResult] = useState<IngestResult | null>(null)

  const onSuccess = (r: IngestResult) => {
    setResult(r)
    qc.invalidateQueries()
  }
  const upload = useMutation({ mutationFn: () => api.upload(file!, service), onSuccess: (r) => { onSuccess(r); setFile(null) } })
  const paste = useMutation({ mutationFn: () => api.ingestText(text, service), onSuccess: (r) => { onSuccess(r); setText('') } })
  const error = upload.error ?? paste.error

  const pick = (f: File | undefined | null) => {
    if (f) { setFile(f); setResult(null) }
  }
  const onDrop = (e: DragEvent) => {
    e.preventDefault()
    setDragging(false)
    pick(e.dataTransfer.files[0])
  }

  return (
    <>
      <PageHeader title="导入日志" description="上传文件或粘贴文本，系统会自动识别格式、聚类出日志模式并检测异常" />

      <div className="mb-4 flex flex-wrap items-center gap-1.5">
        <span className="mr-1 text-xs text-zinc-500">自动识别</span>
        {FORMATS.map((f) => (
          <span key={f} className="rounded-full bg-zinc-100 px-2.5 py-0.5 text-xs text-zinc-600 dark:bg-zinc-800 dark:text-zinc-400">{f}</span>
        ))}
      </div>

      {result && (
        <div className="mb-4 flex flex-wrap items-center gap-3 rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-300">
          <CheckCircle2 className="size-5 shrink-0" />
          <span>已导入 <b className="tabular-nums">{fmtNumber(result.accepted)}</b> 条日志，发现 <b className="tabular-nums">{result.new_patterns}</b> 个新模式；异常检测已在后台开始</span>
          <span className="ml-auto flex gap-2">
            <Link to="/logs" className={clsx(ghostButtonClass, 'py-1 text-xs')}>查看日志</Link>
            <Link to="/anomalies" className={clsx(ghostButtonClass, 'py-1 text-xs')}>查看异常</Link>
          </span>
        </div>
      )}
      {error && <div className="mb-4"><ErrorBox error={error} /></div>}

      <Card className="mb-4" bodyClassName="flex flex-wrap items-center gap-x-4 gap-y-2">
        <label htmlFor="svc" className="text-sm font-medium">默认服务名</label>
        <input id="svc" value={service} onChange={(e) => setService(e.target.value)} placeholder="如 web-gateway" className={`${inputClass} w-64 max-w-full`} />
        <span className="text-xs text-zinc-500">日志本身没有服务名时使用；上传文件时留空则使用文件名</span>
      </Card>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card title="上传文件" bodyClassName="flex flex-col gap-3">
          <div
            role="button"
            tabIndex={0}
            aria-label="选择日志文件"
            onDragOver={(e) => { e.preventDefault(); setDragging(true) }}
            onDragLeave={() => setDragging(false)}
            onDrop={onDrop}
            onClick={() => fileRef.current?.click()}
            onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); fileRef.current?.click() } }}
            className={clsx(
              'flex h-44 cursor-pointer flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed text-sm transition-colors',
              dragging ? 'border-indigo-500 bg-indigo-50 dark:bg-indigo-500/10' : 'border-zinc-300 hover:border-zinc-400 hover:bg-zinc-50 dark:border-zinc-700 dark:hover:border-zinc-600 dark:hover:bg-zinc-800/40',
            )}
          >
            <FileUp className={clsx('size-8', dragging ? 'text-indigo-500' : 'text-zinc-400')} />
            <span className="text-zinc-600 dark:text-zinc-300">{dragging ? '松开以上传' : <>拖拽文件到此处，或<span className="text-indigo-600 dark:text-indigo-400">点击选择</span></>}</span>
            <span className="text-xs text-zinc-400">.log、.txt、.json 等文本文件，单个最大 50 MB</span>
            <input ref={fileRef} type="file" className="hidden" onChange={(e) => { pick(e.target.files?.[0]); e.target.value = '' }} />
          </div>
          {file && (
            <div className="flex items-center gap-2 rounded-lg bg-zinc-50 px-3 py-2 text-sm dark:bg-zinc-800/50">
              <FileText className="size-4 shrink-0 text-zinc-500" />
              <span className="min-w-0 flex-1 truncate">{file.name}</span>
              <span className="text-xs text-zinc-500 tabular-nums">{formatSize(file.size)}</span>
              <button aria-label="移除文件" onClick={() => setFile(null)} className="rounded p-0.5 text-zinc-400 hover:text-zinc-700 dark:hover:text-zinc-200"><X className="size-4" /></button>
            </div>
          )}
          <button className={clsx(buttonClass, 'self-start')} disabled={!file || upload.isPending} onClick={() => upload.mutate()}>
            {upload.isPending ? '上传中…' : '上传并分析'}
          </button>
        </Card>

        <Card
          title="粘贴日志"
          bodyClassName="flex flex-col gap-3"
          actions={<button className="text-xs text-indigo-600 hover:underline dark:text-indigo-400" onClick={() => setText(EXAMPLE)}>填入示例</button>}
        >
          <textarea
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder="每行一条日志，多行异常堆栈会自动合并…"
            aria-label="粘贴日志"
            className={clsx(inputClass, 'h-44 w-full resize-y py-2 font-mono text-xs leading-5')}
          />
          <div className="flex items-center gap-3">
            <button className={buttonClass} disabled={!text.trim() || paste.isPending} onClick={() => paste.mutate()}>
              {paste.isPending ? '导入中…' : '导入'}
            </button>
            {text && <span className="text-xs text-zinc-500 tabular-nums">{fmtNumber(text.split('\n').filter((l) => l.trim()).length)} 行</span>}
          </div>
        </Card>
      </div>

      <Integrations />
    </>
  )
}

type Tab = 'browser' | 'api' | 'file'

function Integrations() {
  const [tab, setTab] = useState<Tab>('browser')
  const origin = window.location.origin
  const snippets: Record<Tab, { desc: string; code: string }> = {
    browser: {
      desc: '在网页 <head> 中尽量靠前的位置加入，自动上报未处理的 JS 异常、未处理的 Promise 拒绝和资源加载失败（含堆栈、页面 URL、浏览器信息）。也可调用 LogPulse.captureError(err) 手动上报。',
      code: `<script src="${origin}/logpulse-browser.js"
        data-endpoint="${origin}/api/logs"
        data-service="web-frontend"
        data-release="1.0.0"></script>`,
    },
    api: {
      desc: '推送 JSON 数组，或任意支持的文本格式（一行一条，多行堆栈会自动合并）。',
      code: `# JSON 数组
curl -X POST ${origin}/api/logs -H 'Content-Type: application/json' \\
  -d '[{"level":"error","service":"api","msg":"db timeout"}]'

# 任意格式文本
tail -n 1000 /var/log/nginx/access.log | \\
  curl -X POST '${origin}/api/logs?service=nginx' --data-binary @-`,
    },
    file: {
      desc: '上传日志文件；service 可选，默认取文件名。',
      code: `curl -F file=@app.log -F service=api ${origin}/api/logs/upload`,
    },
  }
  return (
    <Card
      title="接入方式"
      className="mt-4"
      actions={
        <Segmented<Tab> value={tab} onChange={setTab} size="sm"
          options={[{ value: 'browser', label: '前端错误采集' }, { value: 'api', label: 'HTTP API' }, { value: 'file', label: '文件上传' }]} />
      }
    >
      <p className="mb-3 text-sm text-zinc-600 dark:text-zinc-400">{snippets[tab].desc}</p>
      <div className="relative">
        <pre className={clsx(codeBlockClass, 'pr-20')}>{snippets[tab].code}</pre>
        <CopyButton text={snippets[tab].code} className="absolute top-2 right-2 rounded-md bg-white/10 px-2 py-1 text-xs text-zinc-200 hover:bg-white/20" />
      </div>
    </Card>
  )
}
