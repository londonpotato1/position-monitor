// ============================================================
// PnLDisplay - PnL 값 색상 표시
// ============================================================

import type { FailedExchange } from '../types'

interface Props {
  value: number
  className?: string
  showZero?: boolean
}

export default function PnLDisplay({ value, className = '', showZero = true }: Props) {
  if (!showZero && value === 0) return null

  const isPositive = value > 0
  const isNegative = value < 0

  const color = isPositive
    ? '#3fb950'
    : isNegative
    ? '#f85149'
    : '#768390'

  const sign = isPositive ? '+' : ''
  const formatted = `${sign}${value.toFixed(2)} U`

  return (
    <span
      className={`font-mono tabular-nums ${className}`}
      style={{ color }}
    >
      {formatted}
    </span>
  )
}

// ============================================================
// FailedExchangesBanner - 조회 실패 거래소 배너
// Hedged, Unmatched 2곳에서 공통 사용
// ============================================================

interface BannerProps {
  exchanges: FailedExchange[]
}

export function FailedExchangesBanner({ exchanges }: BannerProps) {
  if (!exchanges || exchanges.length === 0) return null
  const tooltip = exchanges.map(f => `${f.exchange} (${f.scope}): ${f.error}`).join('\n')
  const names = exchanges.map(f => f.exchange).join(', ')
  return (
    <div
      className="px-3 py-2 mb-3 rounded text-xs"
      style={{
        backgroundColor: '#3d1a1a',
        border: '1px solid #f85149',
        color: '#f85149',
      }}
      title={tooltip}
    >
      &#9888; 조회 실패 거래소: {names}
    </div>
  )
}
