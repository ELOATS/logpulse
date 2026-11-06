import { Area, AreaChart, ResponsiveContainer } from 'recharts'

/** 无坐标轴的迷你趋势图 */
export default function Sparkline({ data, color }: { data: number[]; color: string }) {
  if (data.length < 2) return null
  const points = data.map((v, i) => ({ i, v }))
  return (
    <ResponsiveContainer>
      <AreaChart data={points} margin={{ top: 2, right: 0, bottom: 2, left: 0 }}>
        <Area type="monotone" dataKey="v" stroke={color} strokeWidth={1.5} fill={color} fillOpacity={0.12} isAnimationActive={false} />
      </AreaChart>
    </ResponsiveContainer>
  )
}
