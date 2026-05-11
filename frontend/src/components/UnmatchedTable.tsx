// ============================================================
// UnmatchedTable - 미매칭 포지션 테이블
// ============================================================

import React, { useState, useEffect } from 'react'
import type { HedgedPositionLeg } from '../types'
import { usePositionStore } from '../stores/positionStore'
import { getExchangeColor } from './ExchangeBadge'
import PnLDisplay from './PnLDisplay'
import { GetExchangeStatuses } from '../../wailsjs/go/main/App'

const SMALL_THRESHOLD = 1.0

type USortKey = 'exchange' | 'marketType' | 'symbol' | 'side' | 'size' | 'pnl'
type USortDir = 'asc' | 'desc'

function getUSortValue(u: HedgedPositionLeg, key: USortKey): string | number {
  switch (key) {
    case 'exchange': return u.exchange
    case 'marketType': return u.marketType
    case 'symbol': return u.symbol
    case 'side': return u.side
    case 'size': return u.size
    case 'pnl': return u.pnl ?? 0
    default: return 0
  }
}

interface Props {
  unmatched: HedgedPositionLeg[]
}

export default function UnmatchedTable({ unmatched }: Props) {
  const {
    hideSmallUnmatched,
    setHideSmallUnmatched,
    unmatchedTypeFilter,
    setUnmatchedTypeFilter,
    unmatchedExchangeFilter,
    setUnmatchedExchangeFilter,
  } = usePositionStore()

  const [expanded, setExpanded] = useState(true)
  const [uSortKey, setUSortKey] = useState<USortKey>('size')
  const [uSortDir, setUSortDir] = useState<USortDir>('desc')
  const [connectedExchanges, setConnectedExchanges] = useState<string[]>([])

  // 연결된 거래소 목록 (포지션 없어도 필터 버튼에 표시되도록)
  useEffect(() => {
    let mounted = true
    const fetchExchanges = async () => {
      try {
        const statuses = await GetExchangeStatuses()
        if (!mounted || !statuses) return
        const connected = statuses.filter(s => s.connected).map(s => s.name)
        setConnectedExchanges(connected)
      } catch {
        // 실패 시 빈 배열 유지
      }
    }
    fetchExchanges()
    const interval = setInterval(fetchExchanges, 30000)
    return () => { mounted = false; clearInterval(interval) }
  }, [])

  const handleUSort = (key: USortKey) => {
    if (uSortKey === key) {
      setUSortDir(uSortDir === 'asc' ? 'desc' : 'asc')
    } else {
      setUSortKey(key)
      setUSortDir(key === 'pnl' || key === 'size' ? 'desc' : 'asc')
    }
  }

  if (!unmatched || unmatched.length === 0) return null

  const sorted = [...unmatched].sort((a, b) => {
    const va = getUSortValue(a, uSortKey)
    const vb = getUSortValue(b, uSortKey)
    const cmp = typeof va === 'string' ? va.localeCompare(vb as string) : (va as number) - (vb as number)
    const result = uSortDir === 'asc' ? cmp : -cmp
    if (result !== 0) return result
    const ta = `${a.exchange}_${a.symbol}`
    const tb = `${b.exchange}_${b.symbol}`
    return ta.localeCompare(tb)
  })

  const exchanges = [...new Set([
    ...sorted.map(u => u.exchange),
    ...connectedExchanges,
  ])].sort()

  let filtered = sorted
  if (unmatchedTypeFilter !== 'all') {
    filtered = filtered.filter(u => u.marketType === unmatchedTypeFilter)
  }
  if (unmatchedExchangeFilter !== 'all') {
    filtered = filtered.filter(u => u.exchange === unmatchedExchangeFilter)
  }
  const beforeSmall = filtered.length
  if (hideSmallUnmatched) {
    filtered = filtered.filter(u => u.size >= SMALL_THRESHOLD)
  }
  const hiddenCount = beforeSmall - filtered.length

  const filterBtnStyle = (active: boolean) => ({
    backgroundColor: active ? '#1f3f6e' : '#21262d',
    color: active ? '#58a6ff' : '#768390',
    border: '1px solid #30363d',
  })

  return (
    <section className="mb-4">
      {/* 헤더 */}
      <div
        className="flex flex-wrap items-center gap-1.5 px-3 py-2 rounded-t text-xs"
        style={{ backgroundColor: '#161b22', borderBottom: '1px solid #30363d' }}
      >
        {/* 접기/펼치기 */}
        <button
          onClick={() => setExpanded(!expanded)}
          className="font-semibold flex items-center gap-1"
          style={{ color: '#e6edf3' }}
        >
          <span style={{ fontSize: 10 }}>{expanded ? '▼' : '▶'}</span>
          미매칭 ({filtered.length}건{hiddenCount > 0 ? `, ${hiddenCount}건 숨김` : ''})
        </button>

        {/* 타입 필터 */}
        <div className="flex gap-1 ml-2">
          {(['all', 'spot', 'futures'] as const).map(t => (
            <button
              key={t}
              onClick={() => setUnmatchedTypeFilter(t)}
              className="px-2 py-0.5 rounded text-xs"
              style={filterBtnStyle(unmatchedTypeFilter === t)}
            >
              {t === 'all' ? '전체' : t === 'spot' ? '현물' : '선물'}
            </button>
          ))}
        </div>

        {/* 거래소 필터 */}
        <div className="flex gap-1">
          {['all', ...exchanges].map(ex => (
            <button
              key={ex}
              onClick={() => setUnmatchedExchangeFilter(ex)}
              className="px-2 py-0.5 rounded text-xs"
              style={filterBtnStyle(unmatchedExchangeFilter === ex)}
            >
              {ex === 'all' ? '전체' : ex}
            </button>
          ))}
        </div>

        {/* 소액 가리기 */}
        <button
          onClick={() => setHideSmallUnmatched(!hideSmallUnmatched)}
          className="px-2 py-0.5 rounded text-xs ml-auto"
          style={filterBtnStyle(hideSmallUnmatched)}
        >
          {hideSmallUnmatched ? '소액 표시' : '소액 가리기'}
        </button>
      </div>

      {/* 테이블 (접기 가능) */}
      {expanded && (
        <div className="overflow-x-auto rounded-b" style={{ border: '1px solid #30363d', borderTop: 'none' }}>
          <table className="w-full text-xs" style={{ backgroundColor: '#0d1117' }}>
            <thead>
              <tr style={{ backgroundColor: '#161b22', color: '#768390' }}>
                {([
                  ['exchange', '거래소', 'text-left'],
                  ['marketType', '타입', 'text-left'],
                  ['symbol', '심볼', 'text-left'],
                  ['side', '방향', 'text-center'],
                  ['size', '수량', 'text-right'],
                  ['pnl', 'PnL', 'text-right'],
                ] as [USortKey, string, string][]).map(([key, label, align]) => (
                  <th
                    key={key}
                    className={`px-3 py-2 ${align} font-medium cursor-pointer select-none hover:text-white transition-colors`}
                    onClick={() => handleUSort(key)}
                  >
                    {label}
                    {uSortKey === key && (
                      <span className="ml-0.5 text-[10px]">{uSortDir === 'asc' ? '▲' : '▼'}</span>
                    )}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {filtered.map((u, i) => {
                const typeLabel = u.marketType === 'spot' ? '현물' : '선물'
                const sideLabel = u.side === 'holding' ? '보유' : u.side === 'long' ? 'LONG' : 'SHORT'
                const sideColor = u.side === 'long' ? '#3fb950' : u.side === 'short' ? '#f85149' : '#adbac7'
                const key = `${u.exchange}_${u.marketType}_${u.symbol}`
                const hasPnl = u.marketType === 'futures' && u.pnl != null
                const rowBg = i % 2 === 0 ? '#0d1117' : '#0d1420'

                return (
                  <tr
                    key={key}
                    style={{ backgroundColor: rowBg, borderBottom: '1px solid #21262d' }}
                    className="hover:brightness-110 transition-all"
                  >
                    <td className="px-3 py-2">
                      <span style={{ color: getExchangeColor(u.exchange), fontWeight: 600 }}>
                        {u.exchange}
                      </span>
                    </td>
                    <td className="px-3 py-2" style={{ color: '#768390' }}>
                      {typeLabel}
                    </td>
                    <td className="px-3 py-2 font-semibold" style={{ color: '#e6edf3' }}>
                      {u.symbol}
                    </td>
                    <td className="px-3 py-2 text-center font-semibold" style={{ color: sideColor }}>
                      {sideLabel}
                    </td>
                    <td className="px-3 py-2 text-right font-mono" style={{ color: '#adbac7' }}>
                      <div>{u.size.toLocaleString(undefined, { maximumFractionDigits: 4 })}</div>
                      {u.markPrice > 0 && (
                        <div className="text-[10px]" style={{ color: '#484f58' }}>
                          ${(u.size * u.markPrice).toLocaleString(undefined, { maximumFractionDigits: 0 })}
                        </div>
                      )}
                    </td>
                    <td className="px-3 py-2 text-right">
                      {hasPnl ? <PnLDisplay value={u.pnl} /> : <span style={{ color: '#484f58' }}>-</span>}
                    </td>
                  </tr>
                )
              })}
              {filtered.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-3 py-4 text-center" style={{ color: '#768390' }}>
                    표시할 항목이 없습니다
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}
