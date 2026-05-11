// ============================================================
// HedgePairTable - 현물-선물 헷지 페어 테이블
// ============================================================

import React, { useState } from 'react'
import type { HedgedPositionPair } from '../types'
import { usePositionStore } from '../stores/positionStore'
import { getExchangeColor } from './ExchangeBadge'
import PnLDisplay from './PnLDisplay'

const SMALL_QTY_THRESHOLD = 1.0
const EXEMPT_COINS = new Set(['BTC', 'ETH'])

type SortKey = 'coin' | 'spotExchange' | 'size' | 'futuresExchange' | 'direction' | 'matchedSize' | 'pnl'
type SortDir = 'asc' | 'desc'

function getSortValue(p: HedgedPositionPair, key: SortKey): string | number {
  switch (key) {
    case 'coin': return p.coin
    case 'spotExchange': return (p.spotLeg ?? p.longLeg)?.exchange ?? ''
    case 'size': return p.matchedSize
    case 'futuresExchange': return (p.futuresLeg ?? p.shortLeg)?.exchange ?? ''
    case 'direction': return p.direction
    case 'matchedSize': return p.matchedSize
    case 'pnl': return p.futuresPnl ?? 0
    default: return 0
  }
}

interface Props {
  pairs: HedgedPositionPair[]
}

export default function HedgePairTable({ pairs }: Props) {
  const { hideSmallPairs, setHideSmallPairs, refreshPositions } = usePositionStore()
  const [refreshing, setRefreshing] = useState(false)
  const [sortKey, setSortKey] = useState<SortKey>('coin')
  const [sortDir, setSortDir] = useState<SortDir>('asc')

  const handleSort = (key: SortKey) => {
    if (sortKey === key) {
      setSortDir(sortDir === 'asc' ? 'desc' : 'asc')
    } else {
      setSortKey(key)
      setSortDir(key === 'pnl' || key === 'matchedSize' || key === 'size' ? 'desc' : 'asc')
    }
  }

  const sfPairs = pairs.filter(p => p.pairType !== 'futures_futures')
  const allCount = sfPairs.length
  const afterSmall = hideSmallPairs
    ? sfPairs.filter(p => EXEMPT_COINS.has(p.coin.toUpperCase()) || p.matchedSize >= SMALL_QTY_THRESHOLD)
    : sfPairs
  const hiddenCount = allCount - afterSmall.length

  const filtered = [...afterSmall].sort((a, b) => {
    const va = getSortValue(a, sortKey)
    const vb = getSortValue(b, sortKey)
    const cmp = typeof va === 'string' ? va.localeCompare(vb as string) : (va as number) - (vb as number)
    const result = sortDir === 'asc' ? cmp : -cmp
    if (result !== 0) return result
    return a.pairId.localeCompare(b.pairId)
  })

  const totalPnl = sfPairs.reduce((sum, p) => sum + (p.futuresPnl ?? 0), 0)

  const handleRefresh = async () => {
    if (refreshing) return
    setRefreshing(true)
    try { await refreshPositions() } finally { setRefreshing(false) }
  }

  return (
    <section className="mb-4">
      {/* 헤더 */}
      <div
        className="flex items-center gap-2 px-3 py-2 rounded-t text-xs"
        style={{ backgroundColor: '#161b22', borderBottom: '1px solid #30363d' }}
      >
        <span className="font-semibold" style={{ color: '#e6edf3' }}>
          헷지 포지션 쌍 ({filtered.length}{hiddenCount > 0 ? `, ${hiddenCount}건 숨김` : ''})
        </span>

        {/* 새로고침 */}
        <button
          onClick={handleRefresh}
          disabled={refreshing}
          className="text-base leading-none px-1 rounded transition-opacity"
          style={{ color: '#58a6ff', opacity: refreshing ? 0.4 : 1 }}
          title="새로고침"
        >
          ↻
        </button>

        {/* 소액 가리기 */}
        <button
          onClick={() => setHideSmallPairs(!hideSmallPairs)}
          className="px-2 py-0.5 rounded text-xs"
          style={{
            backgroundColor: hideSmallPairs ? '#1f3f6e' : '#21262d',
            color: hideSmallPairs ? '#58a6ff' : '#768390',
            border: '1px solid #30363d',
          }}
        >
          {hideSmallPairs ? '소액 표시' : '소액 가리기'}
        </button>

        {/* 선물 PnL */}
        <span className="ml-auto mr-2 font-mono text-xs" style={{ color: '#768390' }}>
          선물 PnL: <PnLDisplay value={totalPnl} />
        </span>
      </div>

      {/* 테이블 */}
      {filtered.length === 0 ? (
        <div
          className="px-3 py-6 text-center text-xs rounded-b"
          style={{ backgroundColor: '#0d1117', color: '#768390', border: '1px solid #30363d', borderTop: 'none' }}
        >
          헷지 포지션이 없습니다
        </div>
      ) : (
        <div className="overflow-x-auto rounded-b" style={{ border: '1px solid #30363d', borderTop: 'none' }}>
          <table className="w-full text-xs" style={{ backgroundColor: '#0d1117' }}>
            <thead>
              <tr style={{ backgroundColor: '#161b22', color: '#768390' }}>
                {([
                  ['coin', '코인', 'text-left'],
                  ['spotExchange', '현물', 'text-left'],
                  ['size', '보유량', 'text-right'],
                  ['futuresExchange', '선물', 'text-left'],
                  ['direction', '방향', 'text-center'],
                  ['matchedSize', '매칭수량', 'text-right'],
                  ['pnl', '선물PnL', 'text-right'],
                ] as [SortKey, string, string][]).map(([key, label, align]) => (
                  <th
                    key={key}
                    className={`px-3 py-2 ${align} font-medium cursor-pointer select-none hover:text-white transition-colors`}
                    onClick={() => handleSort(key)}
                  >
                    {label}
                    {sortKey === key && (
                      <span className="ml-0.5 text-[10px]">{sortDir === 'asc' ? '▲' : '▼'}</span>
                    )}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {filtered.map((p, i) => {
                const leg1 = p.spotLeg ?? p.longLeg
                const leg2 = p.futuresLeg ?? p.shortLeg
                const dirLabel = p.direction === 'normal' ? 'SHORT' : 'LONG'
                const dirColor = dirLabel === 'SHORT' ? '#f85149' : '#3fb950'
                const rowBg = i % 2 === 0 ? '#0d1117' : '#0d1420'

                return (
                  <tr
                    key={p.pairId}
                    style={{ backgroundColor: rowBg, borderBottom: '1px solid #21262d' }}
                    className="hover:brightness-110 transition-all"
                  >
                    <td className="px-3 py-2 font-semibold" style={{ color: '#e6edf3' }}>
                      {p.coin}
                    </td>
                    <td className="px-3 py-2">
                      <span style={{ color: getExchangeColor(leg1?.exchange ?? ''), fontWeight: 600 }}>
                        {leg1?.exchange ?? '-'}
                      </span>
                    </td>
                    <td className="px-3 py-2 text-right font-mono" style={{ color: '#adbac7' }}>
                      {(leg1?.size ?? 0).toLocaleString(undefined, { maximumFractionDigits: 6 })}
                    </td>
                    <td className="px-3 py-2">
                      <span style={{ color: getExchangeColor(leg2?.exchange ?? ''), fontWeight: 600 }}>
                        {leg2?.exchange ?? '-'}
                      </span>
                    </td>
                    <td className="px-3 py-2 text-center font-semibold" style={{ color: dirColor }}>
                      {dirLabel}
                    </td>
                    <td className="px-3 py-2 text-right font-mono" style={{ color: '#adbac7' }}>
                      {p.matchedSize.toLocaleString(undefined, { maximumFractionDigits: 6 })}
                    </td>
                    <td className="px-3 py-2 text-right">
                      <PnLDisplay value={p.futuresPnl ?? 0} />
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}
