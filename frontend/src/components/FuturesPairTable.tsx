// ============================================================
// FuturesPairTable - 선물-선물 헷지 페어 테이블
// ============================================================

import React from 'react'
import type { HedgedPositionPair } from '../types'
import { getExchangeColor } from './ExchangeBadge'
import PnLDisplay from './PnLDisplay'

interface Props {
  pairs: HedgedPositionPair[]
}

export default function FuturesPairTable({ pairs }: Props) {
  const ffPairs = pairs.filter(p => p.pairType === 'futures_futures')
  if (ffPairs.length === 0) return null

  const totalPnl = ffPairs.reduce((sum, p) => sum + (p.futuresPnl ?? 0), 0)

  return (
    <section className="mb-4">
      {/* 헤더 */}
      <div
        className="flex items-center gap-2 px-3 py-2 rounded-t text-xs"
        style={{ backgroundColor: '#161b22', borderBottom: '1px solid #30363d' }}
      >
        <span className="font-semibold" style={{ color: '#e6edf3' }}>
          선선 헷지 쌍 ({ffPairs.length})
        </span>
        <span className="ml-auto font-mono text-xs" style={{ color: '#768390' }}>
          합산 PnL: <PnLDisplay value={totalPnl} />
        </span>
      </div>

      {/* 테이블 */}
      <div className="overflow-x-auto rounded-b" style={{ border: '1px solid #30363d', borderTop: 'none' }}>
        <table className="w-full text-xs" style={{ backgroundColor: '#0d1117' }}>
          <thead>
            <tr style={{ backgroundColor: '#161b22', color: '#768390' }}>
              <th className="px-3 py-2 text-left font-medium">코인</th>
              <th className="px-3 py-2 text-left font-medium">롱거래소</th>
              <th className="px-3 py-2 text-right font-medium">롱수량</th>
              <th className="px-3 py-2 text-left font-medium">숏거래소</th>
              <th className="px-3 py-2 text-right font-medium">숏수량</th>
              <th className="px-3 py-2 text-right font-medium">매칭수량</th>
              <th className="px-3 py-2 text-right font-medium">합산PnL</th>
            </tr>
          </thead>
          <tbody>
            {ffPairs.map((p, i) => {
              const longLeg = p.longLeg ?? p.spotLeg
              const shortLeg = p.shortLeg ?? p.futuresLeg
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
                    <span style={{ color: getExchangeColor(longLeg?.exchange ?? ''), fontWeight: 600 }}>
                      {longLeg?.exchange ?? '-'}
                    </span>
                  </td>
                  <td className="px-3 py-2 text-right font-mono" style={{ color: '#3fb950' }}>
                    {(longLeg?.size ?? 0).toLocaleString(undefined, { maximumFractionDigits: 6 })}
                  </td>
                  <td className="px-3 py-2">
                    <span style={{ color: getExchangeColor(shortLeg?.exchange ?? ''), fontWeight: 600 }}>
                      {shortLeg?.exchange ?? '-'}
                    </span>
                  </td>
                  <td className="px-3 py-2 text-right font-mono" style={{ color: '#f85149' }}>
                    {(shortLeg?.size ?? 0).toLocaleString(undefined, { maximumFractionDigits: 6 })}
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
    </section>
  )
}
