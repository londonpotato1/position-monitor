// ============================================================
// BalanceHistoryChart - 잔고 히스토리 Area Chart
// Recharts 기반, 기간 선택 + USD/% 모드 토글
// ============================================================

import React, { useCallback, useEffect, useState } from 'react'
import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
} from 'recharts'
import type { BalanceHistoryPoint } from '../types'
import { GetBalanceHistory } from '../../wailsjs/go/main/App'

// 유틸
function fmtUSD(val: number): string {
  if (Math.abs(val) >= 1_000_000) return '$' + (val / 1_000_000).toFixed(2) + 'M'
  if (Math.abs(val) >= 1000) return '$' + val.toLocaleString(undefined, { maximumFractionDigits: 0 })
  return '$' + val.toFixed(2)
}

function fmtDate(iso: string, period: Period): string {
  const d = new Date(iso)
  if (period === '7d') {
    return (
      String(d.getMonth() + 1).padStart(2, '0') +
      '/' +
      String(d.getDate()).padStart(2, '0') +
      ' ' +
      String(d.getHours()).padStart(2, '0') +
      ':' +
      String(d.getMinutes()).padStart(2, '0')
    )
  }
  if (period === 'all') {
    return d.getFullYear() + '/' + String(d.getMonth() + 1).padStart(2, '0')
  }
  return String(d.getMonth() + 1).padStart(2, '0') + '/' + String(d.getDate()).padStart(2, '0')
}

type Period = '7d' | '30d' | '90d' | 'all'
type Mode = 'usd' | 'pct'

interface ChartPoint {
  label: string
  value: number
  rawUSD: number
  rawTimestamp: string
}

// 커스텀 툴팁
interface CustomTooltipProps {
  active?: boolean
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  payload?: any[]
  label?: string
  mode: Mode
}

function CustomTooltip({ active, payload, label, mode }: CustomTooltipProps) {
  if (!active || !payload?.length) return null
  const p = payload[0]
  const pt: ChartPoint = p?.payload

  return (
    <div
      style={{
        backgroundColor: '#1c2128',
        border: '1px solid #30363d',
        borderRadius: 4,
        padding: '6px 10px',
        fontSize: 11,
        lineHeight: 1.6,
      }}
    >
      <div style={{ color: '#768390', marginBottom: 2 }}>{label}</div>
      <div style={{ color: '#e6edf3', fontFamily: 'monospace', fontWeight: 600 }}>
        {mode === 'usd'
          ? fmtUSD(pt?.rawUSD ?? 0)
          : `${(p.value >= 0 ? '+' : '')}${Number(p.value).toFixed(2)}%`}
      </div>
    </div>
  )
}

const PERIODS: { key: Period; label: string }[] = [
  { key: '7d', label: '7D' },
  { key: '30d', label: '30D' },
  { key: '90d', label: '90D' },
  { key: 'all', label: 'ALL' },
]

const ACCENT = '#58a6ff'
const GRID_COLOR = '#21262d'

export default function BalanceHistoryChart() {
  const [period, setPeriod] = useState<Period>('30d')
  const [mode, setMode] = useState<Mode>('usd')
  const [rawData, setRawData] = useState<BalanceHistoryPoint[]>([])
  const [loading, setLoading] = useState(false)

  const fetchHistory = useCallback(async (p: Period) => {
    setLoading(true)
    try {
      const result = await GetBalanceHistory(p)
      if (result) setRawData(result as BalanceHistoryPoint[])
      else setRawData([])
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchHistory(period)
  }, [period, fetchHistory])

  const chartData: ChartPoint[] = React.useMemo(() => {
    if (rawData.length === 0) return []
    const baseUSD = rawData[0].totalUSD
    return rawData.map(pt => ({
      label: fmtDate(pt.timestamp, period),
      rawTimestamp: pt.timestamp,
      rawUSD: pt.totalUSD,
      value:
        mode === 'usd'
          ? pt.totalUSD
          : ((pt.totalUSD - baseUSD) / (baseUSD || 1)) * 100,
    }))
  }, [rawData, mode, period])

  const isEmpty = chartData.length === 0

  const values = chartData.map(d => d.value)
  const minVal = values.length ? Math.min(...values) : 0
  const maxVal = values.length ? Math.max(...values) : 1
  const padding = Math.abs(maxVal - minVal) * 0.1 || 1
  const yMin = minVal - padding
  const yMax = maxVal + padding

  return (
    <section className="mb-4">
      {/* 헤더 */}
      <div
        className="flex items-center justify-between px-3 py-2 rounded-t"
        style={{ backgroundColor: '#161b22', borderBottom: '1px solid #30363d' }}
      >
        <span className="text-xs font-semibold" style={{ color: '#e6edf3' }}>
          📊 내 자산 그래프
          {loading && (
            <span className="ml-2 font-normal" style={{ color: '#58a6ff' }}>
              불러오는 중...
            </span>
          )}
        </span>
        <div className="flex items-center gap-2">
          {/* 기간 버튼 그룹 */}
          <div className="flex gap-0.5">
            {PERIODS.map(({ key, label }) => (
              <button
                key={key}
                onClick={() => setPeriod(key)}
                className="text-xs px-2 py-0.5 rounded transition-colors"
                style={{
                  backgroundColor: period === key ? '#58a6ff22' : 'transparent',
                  color: period === key ? '#58a6ff' : '#768390',
                  border: `1px solid ${period === key ? '#58a6ff44' : 'transparent'}`,
                  cursor: 'pointer',
                }}
              >
                {label}
              </button>
            ))}
          </div>
          {/* USD / % 토글 */}
          <div
            className="flex rounded overflow-hidden"
            style={{ border: '1px solid #30363d' }}
          >
            {(['usd', 'pct'] as Mode[]).map(m => (
              <button
                key={m}
                onClick={() => setMode(m)}
                className="text-xs px-2 py-0.5 transition-colors"
                style={{
                  backgroundColor: mode === m ? '#21262d' : 'transparent',
                  color: mode === m ? '#e6edf3' : '#484f58',
                  cursor: 'pointer',
                  border: 'none',
                }}
              >
                {m === 'usd' ? 'USD' : '%'}
              </button>
            ))}
          </div>
        </div>
      </div>

      {/* 차트 */}
      <div
        className="rounded-b"
        style={{
          backgroundColor: '#0d1117',
          border: '1px solid #30363d',
          borderTop: 'none',
          height: 300,
        }}
      >
        {isEmpty ? (
          <div className="flex items-center justify-center h-full">
            <span className="text-xs" style={{ color: '#484f58' }}>
              데이터 수집 중...
            </span>
          </div>
        ) : (
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart
              data={chartData}
              margin={{ top: 12, right: 12, left: 0, bottom: 4 }}
            >
              <defs>
                <linearGradient id="balanceGrad" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor={ACCENT} stopOpacity={0.25} />
                  <stop offset="95%" stopColor={ACCENT} stopOpacity={0.02} />
                </linearGradient>
              </defs>
              <CartesianGrid
                strokeDasharray="3 3"
                stroke={GRID_COLOR}
                vertical={false}
              />
              <XAxis
                dataKey="label"
                tick={{ fill: '#484f58', fontSize: 10 }}
                tickLine={false}
                axisLine={{ stroke: GRID_COLOR }}
                interval="preserveStartEnd"
              />
              <YAxis
                domain={[yMin, yMax]}
                tick={{ fill: '#484f58', fontSize: 10 }}
                tickLine={false}
                axisLine={false}
                width={mode === 'usd' ? 72 : 50}
                tickFormatter={v =>
                  mode === 'usd'
                    ? fmtUSD(Number(v))
                    : `${Number(v) >= 0 ? '+' : ''}${Number(v).toFixed(1)}%`
                }
              />
              <Tooltip
                content={<CustomTooltip mode={mode} />}
                cursor={{ stroke: '#30363d', strokeWidth: 1 }}
              />
              <Area
                type="monotone"
                dataKey="value"
                stroke={ACCENT}
                strokeWidth={1.5}
                fill="url(#balanceGrad)"
                dot={false}
                activeDot={{ r: 3, fill: ACCENT, strokeWidth: 0 }}
              />
            </AreaChart>
          </ResponsiveContainer>
        )}
      </div>
    </section>
  )
}
