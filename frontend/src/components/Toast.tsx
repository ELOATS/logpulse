import { createContext, useCallback, useContext, useRef, useState, type ReactNode } from 'react'
import { CheckCircle2, X } from 'lucide-react'

type Toast = { id: number; message: ReactNode; action?: { label: string; onClick: () => void } }
const Ctx = createContext<(t: Omit<Toast, 'id'>) => void>(() => {})

/** 轻量提示：右下角（移动端在底部导航上方）显示，5 秒后自动消失，可带"撤销"等操作 */
export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const seq = useRef(0)
  const dismiss = useCallback((id: number) => setToasts((ts) => ts.filter((t) => t.id !== id)), [])
  const show = useCallback((t: Omit<Toast, 'id'>) => {
    const id = ++seq.current
    setToasts((ts) => [...ts.slice(-2), { ...t, id }])
    setTimeout(() => dismiss(id), 5000)
  }, [dismiss])
  return (
    <Ctx.Provider value={show}>
      {children}
      <div aria-live="polite" className="pointer-events-none fixed inset-x-0 bottom-20 z-50 flex flex-col items-center gap-2 px-4 md:inset-x-auto md:right-6 md:bottom-6 md:items-end">
        {toasts.map((t) => (
          <div key={t.id} className="toast-in pointer-events-auto flex items-center gap-3 rounded-lg bg-zinc-900 py-2.5 pr-2 pl-3.5 text-sm text-white shadow-lg ring-1 ring-white/10 dark:bg-zinc-800">
            <CheckCircle2 className="size-4 shrink-0 text-emerald-400" />
            <span>{t.message}</span>
            {t.action && (
              <button
                className="rounded-md px-2 py-1 font-medium text-indigo-300 hover:bg-white/10"
                onClick={() => { t.action!.onClick(); dismiss(t.id) }}
              >
                {t.action.label}
              </button>
            )}
            <button aria-label="关闭" className="rounded-md p-1 text-zinc-400 hover:bg-white/10 hover:text-white" onClick={() => dismiss(t.id)}>
              <X className="size-3.5" />
            </button>
          </div>
        ))}
      </div>
    </Ctx.Provider>
  )
}

export const useToast = () => useContext(Ctx)
