// ============================================================
// Settings - 거래소 연결 상태
// ============================================================

import { useEffect, useState } from 'react'
import { GetExchangeStatuses } from '../../wailsjs/go/main/App'

interface ExchangeStatus {
  name: string
  connected: boolean
  configured: boolean
}

const EXCHANGE_COLORS: Record<string, string> = {
  binance: '#F0B90B',
  bybit: '#F7A600',
  okx: '#ffffff',
  bitget: '#00C0C0',
  upbit: '#0070E0',
  bithumb: '#e84141',
}

function exchangeColor(name: string): string {
  return EXCHANGE_COLORS[name.toLowerCase()] ?? '#8b949e'
}

export default function Settings() {
  const [exchanges, setExchanges] = useState<ExchangeStatus[]>([])
  const [loading, setLoading] = useState(false)

  const fetchStatuses = async () => {
    setLoading(true)
    try {
      const data = await GetExchangeStatuses()
      if (data && data.length > 0) {
        setExchanges(data.map((ex: { name: string; connected: boolean; configured: boolean }) => ({
          name: ex.name.charAt(0).toUpperCase() + ex.name.slice(1),
          connected: ex.connected,
          configured: ex.configured,
        })))
      }
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchStatuses()
  }, [])

  return (
    <div className="min-h-screen p-4" style={{ backgroundColor: '#0d1117', color: '#e6edf3' }}>
      {/* 헤더 */}
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-sm font-semibold tracking-wide" style={{ color: '#adbac7' }}>
          설정
        </h1>
        <div className="flex items-center gap-2">
          {loading && <span className="text-xs" style={{ color: '#58a6ff' }}>로딩 중...</span>}
          <button
            onClick={fetchStatuses}
            disabled={loading}
            className="text-xs px-3 py-1.5 rounded font-medium disabled:opacity-40"
            style={{ backgroundColor: '#21262d', color: '#adbac7', border: '1px solid #30363d' }}
          >
            새로고침
          </button>
        </div>
      </div>

      {/* 거래소 연결 상태 */}
      <section>
        <div
          className="flex items-center px-3 py-2 rounded-t text-xs font-semibold"
          style={{ backgroundColor: '#161b22', borderBottom: '1px solid #30363d', color: '#e6edf3' }}
        >
          거래소 연결 상태
        </div>
        <div
          className="rounded-b"
          style={{ border: '1px solid #30363d', borderTop: 'none', backgroundColor: '#0d1117' }}
        >
          {exchanges.length === 0 && !loading && (
            <div className="px-3 py-4 text-xs text-center" style={{ color: '#484f58' }}>
              거래소 정보를 불러오는 중...
            </div>
          )}
          {exchanges.map((ex, i) => {
            const isLast = i === exchanges.length - 1
            const color = exchangeColor(ex.name)
            const dotColor = ex.connected ? '#3fb950' : ex.configured ? '#d29922' : '#484f58'
            const statusLabel = ex.connected ? '연결됨' : ex.configured ? 'API 설정됨' : '미설정'

            return (
              <div
                key={ex.name}
                className="flex items-center justify-between px-3 py-2.5"
                style={{ borderBottom: isLast ? 'none' : '1px solid #21262d' }}
              >
                <span className="text-xs font-semibold" style={{ color }}>
                  {ex.name}
                </span>
                <div className="flex items-center gap-2">
                  <span
                    className="inline-block rounded-full"
                    style={{ width: '7px', height: '7px', backgroundColor: dotColor }}
                  />
                  <span className="text-xs" style={{ color: '#768390' }}>
                    {statusLabel}
                  </span>
                </div>
              </div>
            )
          })}
        </div>
      </section>
    </div>
  )
}
