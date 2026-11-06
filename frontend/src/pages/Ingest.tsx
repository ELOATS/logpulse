import { useRef, useState, type DragEvent } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { FileUp } from 'lucide-react'
import clsx from 'clsx'
import { api, type IngestResult } from '../lib/api'
import { fmtNumber } from '../lib/format'
import { Card, ErrorBox, PageHeader, buttonClass, inputClass } from '../components/ui'

const EXAMPLE = `{"ts":"2024-05-01T10:00:00Z","level":"error","service":"api","msg":"db timeout after 3000ms"}
2024-05-01 10:00:01 WARN [order-service] stock low for sku 991
127.0.0.1 - - [01/May/2024:10:00:02 +0000] "GET /api/users/42 HTTP/1.1" 500 512 "-" "curl/8.0"
May  1 10:00:03 web-01 sshd[1234]: Failed password for root from 1.2.3.4`

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
  const upload = useMutation({ mutationFn: () => api.upload(file!, service), onSuccess })
  const paste = useMutation({ mutationFn: () => api.ingestText(text, service), onSuccess })
  const error = upload.error ?? paste.error

  const onDrop = (e: DragEvent) => {
    e.preventDefault()
    setDragging(false)
    if (e.dataTransfer.files[0]) setFile(e.dataTransfer.files[0])
  }

  return (
    <>
      <PageHeader title="导入日志" description="自动识别 JSON 行日志、Nginx/Apache 访问日志、Syslog 与常见文本日志格式" />
      <div className="mb-4 max-w-sm">
        <label className="text-sm">
          <span className="mb-1 block text-zinc-600 dark:text-zinc-400">默认服务名（日志中未包含服务名时使用）</span>
          <input value={service} onChange={(e) => setService(e.target.value)} placeholder="如 web-gateway" className={`${inputClass} w-full`} />
        </label>
      </div>
      {result && (
        <div className="mb-4 rounded-md bg-emerald-50 px-4 py-2 text-sm text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300">
          导入成功：{fmtNumber(result.accepted)} 条日志，发现 {result.new_patterns} 个新模式。异常检测已在后台触发。
        </div>
      )}
      {error && <div className="mb-4"><ErrorBox error={error} /></div>}
      <div className="grid gap-4 lg:grid-cols-2">
        <Card title="上传文件">
          <div
            onDragOver={(e) => { e.preventDefault(); setDragging(true) }}
            onDragLeave={() => setDragging(false)}
            onDrop={onDrop}
            onClick={() => fileRef.current?.click()}
            className={clsx(
              'flex h-48 cursor-pointer flex-col items-center justify-center gap-2 rounded-md border-2 border-dashed text-sm text-zinc-500 transition-colors',
              dragging ? 'border-indigo-500 bg-indigo-50 dark:bg-indigo-500/10' : 'border-zinc-300 dark:border-zinc-700',
            )}
          >
            <FileUp className="size-8 text-zinc-400" />
            {file ? <span className="font-medium text-zinc-800 dark:text-zinc-200">{file.name}（{(file.size / 1024).toFixed(1)} KB）</span> : '拖拽日志文件到此处，或点击选择'}
            <input ref={fileRef} type="file" className="hidden" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
          </div>
          <button className={`${buttonClass} mt-3`} disabled={!file || upload.isPending} onClick={() => upload.mutate()}>
            {upload.isPending ? '上传中…' : '上传并分析'}
          </button>
        </Card>
        <Card title="粘贴日志" actions={<button className="text-xs text-indigo-600 hover:underline dark:text-indigo-400" onClick={() => setText(EXAMPLE)}>填入示例</button>}>
          <textarea
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder="每行一条日志…"
            className={`${inputClass} h-48 w-full resize-none font-mono text-xs`}
          />
          <button className={`${buttonClass} mt-3`} disabled={!text.trim() || paste.isPending} onClick={() => paste.mutate()}>
            {paste.isPending ? '导入中…' : '导入'}
          </button>
        </Card>
      </div>
      <Card title="通过 API 推送" className="mt-4">
        <pre className="overflow-x-auto rounded bg-zinc-900 p-3 font-mono text-xs text-zinc-100">{`# JSON 数组
curl -X POST http://localhost:8080/api/logs -H 'Content-Type: application/json' \\
  -d '[{"level":"error","service":"api","msg":"db timeout"}]'

# 任意格式文本，一行一条
tail -n 1000 /var/log/nginx/access.log | \\
  curl -X POST 'http://localhost:8080/api/logs?service=nginx' --data-binary @-`}</pre>
      </Card>
    </>
  )
}
