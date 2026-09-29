// ============================================================
// HedgePairTable - 현물-선물 헷지 페어 테이블
// ============================================================

import React, { useState } from 'react'
import type { HedgedPositionPair } from '../types'
import { usePositionStore } from '../stores/positionStore'
import { getExchangeColor } from './ExchangeBadge'
import PnLDisplay from './PnLDisplay'
import { positionNotional, totalPositionNotional, formatNotional, formatPositionPrice, formatQty } from './positionDisplay'

const SMALL_QTY_THRESHOLD = 1.0
const EXEMPT_COINS = new Set(['BTC', 'ETH'])
const ALERT_ERROR_STYLE = {
  backgroundColor: '#3d1a1a', color: '#f85149',
  borderLeft: '1px solid #30363d', borderRight: '1px solid #30363d', borderBottom: '1px solid #6b1f1f',
}

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
  const { hideSmallPairs, setHideSmallPairs, refreshPositions, liqAlert, liqAlertError, setLiqAlert } = usePositionStore()
  const [refreshing, setRefreshing] = useState(false)
  const [alertToggleError, setAlertToggleError] = useState<string | null>(null)
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

  const totalNotional = totalPositionNotional(sfPairs)
  const totalPnl = sfPairs.reduce((sum, p) => sum + (p.futuresPnl ?? 0), 0)

  // 알림 키 = 선물 거래소 + 원시 선물 심볼. 같은 키의 행은 같은 상태를 보이고 함께 토글된다.
  const watchByKey = new Map((liqAlert?.keys ?? []).map(k => [`${k.exchange} ${k.symbol}`, k.watch]))
  const rowKeys = new Set(sfPairs.map(p => p.futuresLeg ? `${p.futuresLeg.exchange} ${p.futuresLeg.symbol}` : ''))
  const rowlessOnKeys = (liqAlert?.keys ?? []).filter(k => !rowKeys.has(`${k.exchange} ${k.symbol}`))

  const toggleAlert = async (exchange: string, symbol: string, enabled: boolean) => {
    setAlertToggleError(null)
    try {
      await setLiqAlert(exchange, symbol, enabled)
    } catch (err) {
      setAlertToggleError(`알림 ${enabled ? 'ON' : 'OFF'} 실패 (${exchange} ${symbol}): ${err}`)
    }
  }

  const handleRefresh = async () => {
    if (refreshing) return
    setRefreshing(true)
    try { await refreshPositions() } finally { setRefreshing(false) }
  }

  return (
    <section className="mb-4">
      {/* 헤더 */}
      <div
        className="flex flex-wrap items-center gap-2 px-3 py-2 rounded-t text-xs"
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
        <span className="ml-auto mr-2 font-mono text-xs whitespace-nowrap" style={{ color: '#768390' }}>
          선물 PnL: <PnLDisplay value={totalPnl} />
        </span>

        <span className="mr-2 font-mono text-xs whitespace-nowrap" style={{ color: '#adbac7' }}>
          포지션 규모: {formatNotional(totalNotional)} USDT
        </span>
      </div>

      {/* 청산/상승 알림: 조회 실패, 토글 실패, 전달 불가 배너, 행 없는 ON 키 */}
      {liqAlertError && (
        <div className="px-3 py-1.5 text-xs" style={ALERT_ERROR_STYLE}>알림 상태 조회 실패: {liqAlertError}</div>
      )}
      {alertToggleError && (
        <div className="px-3 py-1.5 text-xs" style={ALERT_ERROR_STYLE}>{alertToggleError}</div>
      )}
      {liqAlert && !liqAlert.deliverable && liqAlert.keys.length > 0 && (
        <div className="px-3 py-1.5 text-xs font-semibold" style={ALERT_ERROR_STYLE}>알림 미전송: {liqAlert.reason}</div>
      )}
      {rowlessOnKeys.length > 0 && (
        <div
          className="flex flex-wrap items-center gap-3 px-3 py-1.5 text-xs"
          style={{ backgroundColor: '#0d1117', color: '#768390', borderLeft: '1px solid #30363d', borderRight: '1px solid #30363d', borderBottom: '1px solid #21262d' }}
        >
          <span className="font-semibold">ON · 현재 행 없음</span>
          {rowlessOnKeys.map(k => (
            <span key={`${k.exchange} ${k.symbol}`} className="inline-flex items-center gap-1">
              <span style={{ color: getExchangeColor(k.exchange), fontWeight: 600 }}>{k.exchange}</span>
              <span style={{ color: '#adbac7' }}>{k.symbol}</span>
              {/* 전달 불가면 평가가 멈춰 상태가 굳으므로 표시하지 않음. 행 없는 키의 ok 는 갱신 지연 → 확인 중 */}
              <span style={{ color: '#d29922' }}>({!liqAlert?.deliverable ? '미전송' : k.watch === 'ok' ? '확인 중' : k.watch})</span>
              <button
                onClick={() => toggleAlert(k.exchange, k.symbol, false)}
                className="px-2 py-0.5 rounded text-xs"
                style={{ backgroundColor: '#21262d', color: '#768390', border: '1px solid #30363d' }}
              >
                끄기
              </button>
            </span>
          ))}
        </div>
      )}

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
          <table className="w-full text-xs whitespace-nowrap" style={{ backgroundColor: '#0d1117' }}>
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
                  <React.Fragment key={key}>
                    <th
                      className={`px-3 py-2 ${align} font-medium cursor-pointer select-none hover:text-white transition-colors`}
                      onClick={() => handleSort(key)}
                    >
                      {label}
                      {sortKey === key && (
                        <span className="ml-0.5 text-[10px]">{sortDir === 'asc' ? '▲' : '▼'}</span>
                      )}
                    </th>
                    {key === 'coin' && (
                      <th className="px-3 py-2 text-right font-medium whitespace-nowrap">현재가(마크, USDT)</th>
                    )}
                  </React.Fragment>
                ))}
                <th className="px-3 py-2 text-right font-medium whitespace-nowrap">포지션 규모 (USDT)</th>
                <th className="px-3 py-2 text-right font-medium whitespace-nowrap">청산가 (USDT)</th>
                <th className="px-3 py-2 text-center font-medium">알림</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((p, i) => {
                const leg1 = p.spotLeg ?? p.longLeg
                const leg2 = p.futuresLeg ?? p.shortLeg
                const dirLabel = p.direction === 'normal' ? 'SHORT' : 'LONG'
                const dirColor = dirLabel === 'SHORT' ? '#f85149' : '#3fb950'
                const rowBg = i % 2 === 0 ? '#0d1117' : '#0d1420'
                const fLeg = p.futuresLeg
                const alertWatch = fLeg ? watchByKey.get(`${fLeg.exchange} ${fLeg.symbol}`) : undefined // undefined = OFF
                const alertOn = alertWatch !== undefined
                const alertSupported = !!fLeg && !!liqAlert?.supportedExchanges.includes(fLeg.exchange)

                return (
                  <tr
                    key={p.pairId}
                    style={{ backgroundColor: rowBg, borderBottom: '1px solid #21262d' }}
                    className="hover:brightness-110 transition-all"
                  >
                    <td className="px-3 py-2 font-semibold" style={{ color: '#e6edf3' }}>
                      {p.coin}
                    </td>
                    <td className="px-3 py-2 text-right font-mono whitespace-nowrap" style={{ color: '#adbac7' }}>
                      {formatPositionPrice(leg2, 'markPrice')}
                    </td>
                    <td className="px-3 py-2">
                      <span style={{ color: getExchangeColor(leg1?.exchange ?? ''), fontWeight: 600 }}>
                        {leg1?.exchange ?? '-'}
                      </span>
                    </td>
                    <td className="px-3 py-2 text-right font-mono" style={{ color: '#adbac7' }}>
                      {formatQty(leg1?.size ?? 0)}
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
                      {formatQty(p.matchedSize)}
                    </td>
                    <td className="px-3 py-2 text-right">
                      <PnLDisplay value={p.futuresPnl ?? 0} />
                    </td>
                    <td className="px-3 py-2 text-right font-mono whitespace-nowrap" style={{ color: '#adbac7' }}>
                      {formatNotional(positionNotional(leg2))}
                    </td>
                    <td className="px-3 py-2 text-right font-mono whitespace-nowrap" style={{ color: '#adbac7' }}>
                      {formatPositionPrice(leg2, 'liquidationPrice')}
                    </td>
                    <td className="px-3 py-2 text-center whitespace-nowrap">
                      <button
                        disabled={!alertSupported}
                        onClick={() => fLeg && toggleAlert(fLeg.exchange, fLeg.symbol, !alertOn)}
                        className="px-2 py-0.5 rounded text-xs disabled:opacity-40"
                        style={{
                          backgroundColor: alertOn ? '#1f3f6e' : '#21262d',
                          color: alertOn ? '#58a6ff' : '#768390',
                          border: '1px solid #30363d',
                        }}
                      >
                        {!liqAlert ? '-' : !alertSupported ? '알림 미지원' : alertOn ? '알림 ON' : '알림 OFF'}
                      </button>
                      {liqAlert?.deliverable && alertWatch && alertWatch !== 'ok' && alertWatch !== '확인 중' && (
                        <span className="ml-1.5" style={{ color: '#d29922' }}>{alertWatch}</span>
                      )}
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
