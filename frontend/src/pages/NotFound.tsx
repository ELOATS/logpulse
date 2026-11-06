import { Link } from 'react-router'
import { Compass } from 'lucide-react'
import { PageHeader, buttonClass } from '../components/ui'

export default function NotFound() {
  return (
    <>
      <PageHeader title="页面不存在" />
      <div className="flex flex-col items-center gap-3 py-16 text-center">
        <Compass className="size-10 text-zinc-300 dark:text-zinc-600" />
        <p className="text-sm text-zinc-500">你访问的页面不存在，可能是链接有误或已被移动。</p>
        <Link to="/" className={buttonClass}>返回总览</Link>
      </div>
    </>
  )
}
