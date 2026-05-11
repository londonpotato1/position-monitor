// ============================================================
// ExchangeBadge - 거래소명 브랜드 컬러 배지
// ============================================================

import React from 'react'

const EXCHANGE_COLORS: Record<string, { color: string; bg: string; border?: string }> = {
  binance:      { color: '#0d1117', bg: '#F0B90B' },
  bybit:        { color: '#0d1117', bg: '#F7A600' },
  okx:          { color: '#FFFFFF', bg: 'transparent', border: '#FFFFFF' },
  gate:         { color: '#0d1117', bg: '#17E6A1' },
  'gate.io':    { color: '#0d1117', bg: '#17E6A1' },
  bitget:       { color: '#0d1117', bg: '#00F0FF' },
  htx:          { color: '#FFFFFF', bg: '#2B6DED' },
  huobi:        { color: '#FFFFFF', bg: '#2B6DED' },
  hyperliquid:  { color: '#FFFFFF', bg: '#A855F7' },
  mexc:         { color: '#0d1117', bg: '#00D084' },
  kucoin:       { color: '#FFFFFF', bg: '#23AF91' },
  upbit:        { color: '#FFFFFF', bg: '#004FFF' },
  bithumb:      { color: '#0d1117', bg: '#F2A024' },
}

const DISPLAY_NAMES: Record<string, string> = {
  binance:     'Binance',
  bybit:       'Bybit',
  okx:         'OKX',
  gate:        'Gate',
  'gate.io':   'Gate',
  bitget:      'Bitget',
  htx:         'HTX',
  huobi:       'HTX',
  hyperliquid: 'HL',
  mexc:        'MEXC',
  kucoin:      'KuCoin',
  upbit:       'Upbit',
  bithumb:     'Bithumb',
}

interface Props {
  exchange: string
  className?: string
}

export default function ExchangeBadge({ exchange, className = '' }: Props) {
  const key = exchange.toLowerCase()
  const style = EXCHANGE_COLORS[key]
  const label = DISPLAY_NAMES[key] ?? exchange

  if (!style) {
    return (
      <span className={`text-xs font-semibold px-1.5 py-0.5 rounded ${className}`}
        style={{ color: '#adbac7' }}>
        {label}
      </span>
    )
  }

  return (
    <span
      className={`text-xs font-semibold px-1.5 py-0.5 rounded ${className}`}
      style={{
        color: style.color,
        backgroundColor: style.bg,
        border: style.border ? `1px solid ${style.border}` : undefined,
      }}
    >
      {label}
    </span>
  )
}

// 색상만 반환하는 헬퍼 (테이블 텍스트 컬러용)
export function getExchangeColor(exchange: string): string {
  const key = exchange.toLowerCase()
  const style = EXCHANGE_COLORS[key]
  if (!style || style.bg === 'transparent') return style?.border ?? '#adbac7'
  return style.bg
}
