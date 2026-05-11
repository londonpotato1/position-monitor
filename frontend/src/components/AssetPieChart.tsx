// ============================================================
// AssetPieChart - 자산/거래소 분배 도넛 차트
// Recharts 기반, 거래소별 / 자산별 모드 토글
// ============================================================

import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { PieChart, Pie, Cell, Tooltip, ResponsiveContainer } from 'recharts'
import type { AssetDistributionItem, ExchangeSummary } from '../types'
import { getExchangeColor } from './ExchangeBadge'
import { GetAssetDistribution } from '../../wailsjs/go/main/App'

// 자산별 자동 컬러 팔레트 (다크 테마 친화)
const ASSET_PALETTE = [
  '#f7931a', // BTC
  '#627eea', // ETH
  '#26a17b', // USDT
  '#2775ca', // USDC
  '#e84142', // AVAX
  '#9945ff', // SOL
  '#00ffa3', // SOL alt
  '#f0b90b', // BNB
  '#cc2936', // others
]

function fmtUSD(val: number): string {
  if (Math.abs(val) >= 1_000_000) return '$' + (val / 1_000_000).toFixed(2) + 'M'
  if (Math.abs(val) >= 1000) return '$' + val.toLocaleString(undefined, { maximumFractionDigits: 0 })
  return '$' + val.toFixed(2)
}

type Mode = 'exchange' | 'asset'

interface PieItem {
  name: string
  value: number
  usdValue: number
  color: string
  pct: number
}

interface CustomTooltipProps {
  active?: boolean
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  payload?: any[]
}

function CustomTooltip({ active, payload }: CustomTooltipProps) {
  if (!active || !payload?.length) return null
  const item: PieItem = payload[0].payload
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
      <div style={{ color: item.color, fontWeight: 600 }}>{item.name}</div>
      <div style={{ color: '#adbac7', fontFamily: 'monospace' }}>
        {fmtUSD(item.usdValue)}
      </div>
      <div style={{ color: '#768390' }}>{item.pct.toFixed(1)}%</div>
    </div>
  )
}

interface Props {
  exchanges?: ExchangeSummary[]
}

export default function AssetPieChart({ exchanges = [] }: Props) {
  const [mode, setMode] = useState<Mode>('exchange')
  const [assetData, setAssetData] = useState<AssetDistributionItem[]>([])
  const [loading, setLoading] = useState(false)
  const [collapsed, setCollapsed] = useState(false)

  const fetchAssets = useCallback(async () => {
    setLoading(true)
    try {
      const data = await GetAssetDistribution()
      if (data && Array.isArray(data)) {
        setAssetData(data as AssetDistributionItem[])
      }
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (!collapsed && mode === 'asset') {
      fetchAssets()
    }
  }, [mode, collapsed, fetchAssets])

  // 거래소별 데이터 (PortfolioSummary.exchanges에서 직접)
  const exchangePieData: PieItem[] = useMemo(() => {
    if (exchanges.length === 0) return []
    const total = exchanges.reduce((s, e) => s + e.totalUSD, 0) || 1
    return exchanges
      .slice()
      .sort((a, b) => b.totalUSD - a.totalUSD)
      .map(e => ({
        name: e.exchange.charAt(0).toUpperCase() + e.exchange.slice(1),
        value: e.totalUSD,
        usdValue: e.totalUSD,
        color: getExchangeColor(e.exchange),
        pct: (e.totalUSD / total) * 100,
      }))
  }, [exchanges])

  // 자산별 데이터: Top 8 + 기타
  const assetPieData: PieItem[] = useMemo(() => {
    if (assetData.length === 0) return []
    const sorted = assetData.slice().sort((a, b) => b.usdValue - a.usdValue)
    const top = sorted.slice(0, 8)
    const rest = sorted.slice(8)
    const restTotal = rest.reduce((s, x) => s + x.usdValue, 0)
    const total = assetData.reduce((s, x) => s + x.usdValue, 0) || 1

    const items: PieItem[] = top.map((x, i) => ({
      name: x.asset,
      value: x.usdValue,
      usdValue: x.usdValue,
      color: ASSET_PALETTE[i % ASSET_PALETTE.length],
      pct: (x.usdValue / total) * 100,
    }))

    if (restTotal > 0) {
      items.push({
        name: '기타',
        value: restTotal,
        usdValue: restTotal,
        color: '#484f58',
        pct: (restTotal / total) * 100,
      })
    }
    return items
  }, [assetData])

  const pieData = mode === 'exchange' ? exchangePieData : assetPieData
  const isEmpty = pieData.length === 0

  return (
    <section className="mb-4">
      {/* 헤더 */}
      <div
        className="flex items-center justify-between px-3 py-2 rounded-t"
        style={{ backgroundColor: '#161b22', borderBottom: '1px solid #30363d' }}
      >
        <button
          onClick={() => setCollapsed(c => !c)}
          className="flex items-center gap-2 text-left"
          style={{ background: 'none', border: 'none', padding: 0, cursor: 'pointer' }}
        >
          <span className="text-xs font-semibold" style={{ color: '#e6edf3' }}>
            🥧 어디에 돈이 있나
          </span>
          <span style={{ color: '#484f58', fontSize: 10 }}>
            {collapsed ? '\u25B6' : '\u25BC'}
          </span>
          {loading && (
            <span className="ml-1 text-xs font-normal" style={{ color: '#58a6ff' }}>
              불러오는 중...
            </span>
          )}
        </button>
        {/* 모드 토글 */}
        <div
          className="flex rounded overflow-hidden"
          style={{ border: '1px solid #30363d' }}
        >
          {(['exchange', 'asset'] as Mode[]).map(m => (
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
              {m === 'exchange' ? '거래소로 보기' : '코인으로 보기'}
            </button>
          ))}
        </div>
      </div>

      {/* 차트 + 레전드 */}
      {!collapsed && (
        <div
          className="rounded-b"
          style={{
            backgroundColor: '#0d1117',
            border: '1px solid #30363d',
            borderTop: 'none',
            padding: '12px 16px',
          }}
        >
          {isEmpty ? (
            <div className="flex items-center justify-center" style={{ height: 180 }}>
              <span className="text-xs" style={{ color: '#484f58' }}>데이터 없음</span>
            </div>
          ) : (
            <div className="flex items-center gap-6">
              {/* 도넛 차트 */}
              <div style={{ width: 200, height: 200, flexShrink: 0 }}>
                <ResponsiveContainer width="100%" height="100%">
                  <PieChart>
                    <Pie
                      data={pieData}
                      cx="50%"
                      cy="50%"
                      innerRadius={60}
                      outerRadius={90}
                      paddingAngle={2}
                      dataKey="value"
                      stroke="none"
                    >
                      {pieData.map((entry, idx) => (
                        <Cell key={`cell-${idx}`} fill={entry.color} opacity={0.9} />
                      ))}
                    </Pie>
                    <Tooltip content={<CustomTooltip />} />
                  </PieChart>
                </ResponsiveContainer>
              </div>

              {/* 레전드 */}
              <div className="flex-1 flex flex-col gap-1.5" style={{ minWidth: 0 }}>
                {pieData.map(item => (
                  <div key={item.name} className="flex items-center gap-2">
                    <div
                      style={{
                        width: 8,
                        height: 8,
                        borderRadius: 2,
                        backgroundColor: item.color,
                        flexShrink: 0,
                      }}
                    />
                    <span
                      className="text-xs font-semibold truncate"
                      style={{ color: '#adbac7', minWidth: 60 }}
                    >
                      {item.name}
                    </span>
                    <span className="text-xs font-mono ml-auto shrink-0" style={{ color: '#768390' }}>
                      {item.pct.toFixed(1)}%
                    </span>
                    <span
                      className="text-xs font-mono w-20 text-right shrink-0"
                      style={{ color: '#adbac7' }}
                    >
                      {fmtUSD(item.usdValue)}
                    </span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </section>
  )
}
