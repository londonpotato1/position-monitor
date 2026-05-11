// ============================================================
// Dashboard - 내 돈이 지금 얼마인지 한눈에 보여주는 화면
// 초보자도 바로 이해할 수 있게 용어를 일상어로 씀
// ============================================================

import React, { useEffect, useRef, useState, useCallback } from 'react'
import type { PortfolioSummary, DailySnapshot, Change24h, CumulativeReturn } from '../types'
import BalanceHistoryChart from '../components/BalanceHistoryChart'
import BTCPriceChart from '../components/BTCPriceChart'
import AssetPieChart from '../components/AssetPieChart'
import {
  GetPortfolioSummary,
  GetPortfolioSnapshots,
  GetChange24h as GoGetChange24h,
  GetCumulativeReturn as GoGetCumulativeReturn,
  TakeSnapshotNow,
} from '../../wailsjs/go/main/App'

const REFRESH_INTERVAL_MS = 5000

function fmtUSD(val: number): string {
  if (Math.abs(val) >= 1000) {
    return '$' + val.toLocaleString(undefined, { maximumFractionDigits: 0 })
  }
  return '$' + val.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

function fmtPnL(val: number): string {
  const prefix = val >= 0 ? '+' : ''
  return prefix + fmtUSD(val)
}

function pnlColor(val: number): string {
  if (val > 0) return '#3fb950'
  if (val < 0) return '#f85149'
  return '#8b949e'
}

function fmtPct(val: number): string {
  return (val >= 0 ? '+' : '') + val.toFixed(2) + '%'
}

interface InfoCardProps {
  label: string
  hint?: string
  value: string
  valueColor?: string
  sub?: string
}

function InfoCard({ label, hint, value, valueColor, sub }: InfoCardProps) {
  return (
    <div
      className="rounded px-3 py-3 flex flex-col gap-1"
      style={{ backgroundColor: '#161b22', border: '1px solid #30363d' }}
    >
      <div className="flex items-baseline gap-2">
        <span className="text-xs font-medium" style={{ color: '#adbac7' }}>{label}</span>
        {hint && <span className="text-[10px]" style={{ color: '#6e7681' }}>{hint}</span>}
      </div>
      <span className="text-sm font-semibold font-mono" style={{ color: valueColor ?? '#e6edf3' }}>
        {value}
      </span>
      {sub && <span className="text-xs" style={{ color: '#484f58' }}>{sub}</span>}
    </div>
  )
}

export default function Dashboard() {
  const [summary, setSummary] = useState<PortfolioSummary | null>(null)
  const [snapshots, setSnapshots] = useState<DailySnapshot[]>([])
  const [change24h, setChange24h] = useState<Change24h | null>(null)
  const [cumReturn, setCumReturn] = useState<CumulativeReturn | null>(null)
  const [loading, setLoading] = useState(false)
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const fetchData = useCallback(async () => {
    setLoading(true)
    try {
      const [summaryData, snapshotData, changeData, cumData] = await Promise.allSettled([
        GetPortfolioSummary(),
        GetPortfolioSnapshots(30),
        GoGetChange24h(),
        GoGetCumulativeReturn(),
      ])
      if (summaryData.status === 'fulfilled' && summaryData.value) setSummary(summaryData.value as PortfolioSummary)
      if (snapshotData.status === 'fulfilled' && snapshotData.value) setSnapshots(snapshotData.value as DailySnapshot[])
      if (changeData.status === 'fulfilled' && changeData.value) setChange24h(changeData.value as Change24h)
      if (cumData.status === 'fulfilled' && cumData.value) setCumReturn(cumData.value as CumulativeReturn)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchData()
    intervalRef.current = setInterval(fetchData, REFRESH_INTERVAL_MS)
    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current)
    }
  }, [fetchData])

  const exchanges = summary?.exchanges ?? []

  // 안전 상태 판정: 매칭률이 높을수록 코인값 하락 위험이 상쇄됨 (헷지됨)
  const matchRate = summary?.matchRate ?? 0
  const unmatchedCount = summary?.unmatchedCount ?? 0
  const safetyStatus = (() => {
    if (!summary) return null
    if (matchRate >= 95 && unmatchedCount === 0) {
      return { icon: '✅', label: '안전', color: '#3fb950', msg: '보유 코인 대부분이 선물로 보호되고 있어요.' }
    }
    if (matchRate >= 80) {
      return { icon: '🟡', label: '대체로 안전', color: '#d29922', msg: `${unmatchedCount}개 코인만 선물 보호가 없어요.` }
    }
    return { icon: '⚠️', label: '점검 필요', color: '#f85149', msg: `보호 안 된 코인이 ${unmatchedCount}개 있어요. 미매칭 탭을 확인하세요.` }
  })()

  return (
    <div className="min-h-screen p-4" style={{ backgroundColor: '#0d1117', color: '#e6edf3' }}>
      {/* 헤더 */}
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-sm font-semibold tracking-wide" style={{ color: '#adbac7' }}>
          내 투자 현황
        </h1>
        <div className="flex items-center gap-3">
          {loading && (
            <span className="text-xs" style={{ color: '#58a6ff' }}>불러오는 중...</span>
          )}
          {summary?.updatedAt && (
            <span className="text-xs" style={{ color: '#484f58' }}>
              마지막 확인: {summary.updatedAt}
            </span>
          )}
          <button
            onClick={() => TakeSnapshotNow()}
            disabled={loading}
            className="text-xs px-2 py-0.5 rounded transition-opacity"
            style={{
              backgroundColor: '#21262d',
              border: '1px solid #30363d',
              color: '#768390',
              opacity: loading ? 0.4 : 1,
              cursor: 'pointer',
            }}
            title="오늘 잔고를 기록으로 남깁니다"
          >
            오늘 잔고 기록
          </button>
          <button
            onClick={fetchData}
            disabled={loading}
            className="text-base leading-none px-1 rounded transition-opacity"
            style={{ color: '#58a6ff', opacity: loading ? 0.4 : 1 }}
            title="최신 정보로 새로고침"
          >
            ↻
          </button>
        </div>
      </div>

      {/* Hero: 가장 중요한 "지금 내 돈 얼마인가" */}
      <div
        className="rounded-lg p-5 mb-4"
        style={{
          background: 'linear-gradient(135deg, #161b22 0%, #1c2430 100%)',
          border: '1px solid #30363d',
        }}
      >
        <div className="flex items-baseline gap-2 mb-2">
          <span className="text-xs" style={{ color: '#768390' }}>💰 지금 내 돈</span>
          <span className="text-[10px]" style={{ color: '#6e7681' }}>
            (모든 거래소에 있는 돈을 달러로 합친 값)
          </span>
        </div>
        <div className="text-3xl font-bold font-mono mb-3" style={{ color: '#e6edf3' }}>
          {summary ? fmtUSD(summary.totalAssetsUSD) : '—'}
        </div>

        <div className="grid grid-cols-2 gap-3 pt-3" style={{ borderTop: '1px solid #21262d' }}>
          {/* 오늘의 변화 */}
          <div>
            <div className="text-xs mb-1" style={{ color: '#768390' }}>
              📅 어제보다
            </div>
            {change24h ? (
              <div className="font-mono text-sm font-semibold" style={{ color: pnlColor(change24h.changeUSD) }}>
                {change24h.changeUSD >= 0 ? '▲ ' : '▼ '}
                {fmtPnL(change24h.changeUSD)}
                <span className="ml-2 text-xs">({fmtPct(change24h.changePct)})</span>
              </div>
            ) : (
              <div className="text-sm" style={{ color: '#484f58' }}>—</div>
            )}
          </div>

          {/* 시작 이후 전체 수익 */}
          <div>
            <div className="text-xs mb-1" style={{ color: '#768390' }}>
              🎯 시작한 이후 총 수익
            </div>
            {cumReturn ? (
              <div className="font-mono text-sm font-semibold" style={{ color: pnlColor(cumReturn.returnUSD) }}>
                {cumReturn.returnUSD >= 0 ? '▲ ' : '▼ '}
                {fmtPnL(cumReturn.returnUSD)}
                <span className="ml-2 text-xs">({fmtPct(cumReturn.returnPct)})</span>
                <div className="text-[10px] mt-0.5" style={{ color: '#6e7681' }}>
                  {cumReturn.firstSnapshotDate}부터 계산
                </div>
              </div>
            ) : (
              <div className="text-sm" style={{ color: '#484f58' }}>—</div>
            )}
          </div>
        </div>
      </div>

      {/* 안전 상태 배너 */}
      {safetyStatus && (
        <div
          className="rounded p-3 mb-4 flex items-start gap-3"
          style={{ backgroundColor: '#161b22', border: `1px solid ${safetyStatus.color}40` }}
        >
          <div className="text-xl leading-none">{safetyStatus.icon}</div>
          <div className="flex-1">
            <div className="flex items-center gap-2 mb-0.5">
              <span className="text-sm font-semibold" style={{ color: safetyStatus.color }}>
                {safetyStatus.label}
              </span>
              <span className="text-xs font-mono" style={{ color: '#768390' }}>
                보호율 {matchRate.toFixed(0)}%
              </span>
            </div>
            <div className="text-xs" style={{ color: '#adbac7' }}>{safetyStatus.msg}</div>
            <div className="text-[10px] mt-1" style={{ color: '#6e7681' }}>
              💡 선물로 보호된 코인은 가격이 떨어져도 손실이 상쇄돼요. (현물 매수 + 선물 매도 = 가격 변동 무관)
            </div>
          </div>
        </div>
      )}

      {/* 돈이 어떻게 나뉘어 있나 */}
      <div className="text-xs mb-2 mt-5" style={{ color: '#768390' }}>
        돈이 어떻게 나뉘어 있나
      </div>
      <div className="grid grid-cols-3 gap-2 mb-4">
        <InfoCard
          label="💎 보유 코인"
          hint="(실제 가지고 있는 암호화폐)"
          value={summary ? fmtUSD(summary.totalSpotUSD) : '—'}
        />
        <InfoCard
          label="🔄 선물 증거금"
          hint="(선물 거래에 담보로 맡긴 돈)"
          value={summary ? fmtUSD(summary.totalFuturesUSD) : '—'}
        />
        <InfoCard
          label="📈 아직 안 판 수익"
          hint="(지금 팔면 받게 될 금액)"
          value={summary ? fmtPnL(summary.totalUnrealizedPnl) : '—'}
          valueColor={summary ? pnlColor(summary.totalUnrealizedPnl) : undefined}
        />
      </div>

      {/* 포지션 상태 */}
      <div className="text-xs mb-2 mt-5" style={{ color: '#768390' }}>
        포지션 상태
      </div>
      <div className="grid grid-cols-2 gap-2 mb-4">
        <InfoCard
          label="🛡️ 짝 맞춘 포지션"
          hint="(현물+선물로 위험 상쇄된 쌍)"
          value={summary ? `${summary.hedgedPairCount}쌍` : '—'}
          sub={summary ? `전체의 ${matchRate.toFixed(0)}%가 보호됨` : undefined}
        />
        <InfoCard
          label="⚠️ 혼자 있는 포지션"
          hint="(선물 짝이 없어 가격 변동에 그대로 노출)"
          value={summary ? `${summary.unmatchedCount}개` : '—'}
          valueColor={summary && summary.unmatchedCount > 0 ? '#d29922' : undefined}
          sub={summary && summary.unmatchedCount > 0 ? '미매칭 탭에서 확인' : undefined}
        />
      </div>

      {/* 차트들 */}
      <div className="text-xs mb-2 mt-5" style={{ color: '#768390' }}>
        내 자산은 어떻게 변하고 있나
      </div>
      <BalanceHistoryChart />

      <div className="text-xs mb-2 mt-5" style={{ color: '#768390' }}>
        비트코인 가격 (시장 기준)
      </div>
      <BTCPriceChart />

      <div className="text-xs mb-2 mt-5" style={{ color: '#768390' }}>
        돈이 어느 거래소에 있나
      </div>
      <AssetPieChart exchanges={exchanges} />

      {/* 매일 기록 */}
      <section className="mt-5">
        <div
          className="flex items-center px-3 py-2 rounded-t text-xs font-semibold"
          style={{ backgroundColor: '#161b22', borderBottom: '1px solid #30363d', color: '#e6edf3' }}
        >
          <span>📆 하루하루 기록 (최근 30일)</span>
          <span className="ml-2 text-[10px] font-normal" style={{ color: '#6e7681' }}>
            매일 잔고와 그날 수익을 저장한 표예요
          </span>
        </div>
        <div
          className="rounded-b overflow-x-auto"
          style={{ border: '1px solid #30363d', borderTop: 'none' }}
        >
          {snapshots.length === 0 ? (
            <p className="text-xs text-center py-6" style={{ color: '#484f58' }}>
              아직 기록이 없어요. 위의 "오늘 잔고 기록" 버튼을 눌러보세요.
            </p>
          ) : (
            <table className="w-full text-xs" style={{ backgroundColor: '#0d1117' }}>
              <thead>
                <tr style={{ backgroundColor: '#161b22', color: '#768390' }}>
                  <th className="px-3 py-2 text-left font-medium">날짜</th>
                  <th className="px-3 py-2 text-right font-medium">그날의 내 전체 돈</th>
                  <th className="px-3 py-2 text-right font-medium">그날 번 돈 (또는 잃은 돈)</th>
                </tr>
              </thead>
              <tbody>
                {snapshots
                  .slice()
                  .sort((a, b) => b.date.localeCompare(a.date))
                  .map((s, i) => (
                    <tr
                      key={s.date}
                      style={{
                        backgroundColor: i % 2 === 0 ? '#0d1117' : '#0d1420',
                        borderBottom: '1px solid #21262d',
                      }}
                    >
                      <td className="px-3 py-1.5" style={{ color: '#8b949e' }}>{s.date}</td>
                      <td className="px-3 py-1.5 text-right font-mono" style={{ color: '#adbac7' }}>
                        {fmtUSD(s.totalAssetsUSD)}
                      </td>
                      <td className="px-3 py-1.5 text-right font-mono" style={{ color: pnlColor(s.totalPnl) }}>
                        {fmtPnL(s.totalPnl)}
                      </td>
                    </tr>
                  ))}
              </tbody>
            </table>
          )}
        </div>
      </section>
    </div>
  )
}
