import { useEffect, useRef } from 'react'
import { usePositionStore } from '../stores/positionStore'
import HedgePairTable from '../components/HedgePairTable'
import FuturesPairTable from '../components/FuturesPairTable'
import { FailedExchangesBanner } from '../components/PnLDisplay'

const REFRESH_INTERVAL_MS = 5000

export default function Hedged() {
  const { pairs, updatedAt, loading, error, failedExchanges, fetchPositions } = usePositionStore()
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)

  useEffect(() => {
    fetchPositions()
    intervalRef.current = setInterval(() => fetchPositions(), REFRESH_INTERVAL_MS)
    return () => { if (intervalRef.current) clearInterval(intervalRef.current) }
  }, [fetchPositions])

  const sfPairs = pairs.filter(p => p.pairType !== 'futures_futures')
  const ffPairs = pairs.filter(p => p.pairType === 'futures_futures')
  const isEmpty = sfPairs.length === 0 && ffPairs.length === 0

  return (
    <div className="min-h-screen p-4" style={{ backgroundColor: '#0d1117', color: '#e6edf3' }}>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-sm font-semibold tracking-wide" style={{ color: '#adbac7' }}>
          헷지 포지션
        </h1>
        <div className="flex items-center gap-3">
          {loading && (
            <span className="text-xs" style={{ color: '#58a6ff' }}>갱신 중...</span>
          )}
          {updatedAt && (
            <span className="text-xs" style={{ color: '#484f58' }}>갱신: {updatedAt}</span>
          )}
        </div>
      </div>

      {error && (
        error.includes('not initialized') || error.includes('초기화') ? (
          <div
            className="text-xs px-3 py-2 rounded mb-3"
            style={{ backgroundColor: '#112138', color: '#58a6ff', border: '1px solid #1f4b82' }}
          >
            초기화 중...
          </div>
        ) : (
          <div
            className="text-xs px-3 py-2 rounded mb-3"
            style={{ backgroundColor: '#3d1a1a', color: '#f85149', border: '1px solid #6b1f1f' }}
          >
            오류: {error}
          </div>
        )
      )}

      <FailedExchangesBanner exchanges={failedExchanges} />

      {isEmpty && !loading && (
        <div
          className="flex flex-col items-center justify-center py-16 rounded"
          style={{ backgroundColor: '#161b22', border: '1px solid #30363d', color: '#768390' }}
        >
          <div className="text-2xl mb-2">☐</div>
          <p className="text-xs">헷지 포지션이 없습니다</p>
        </div>
      )}

      {sfPairs.length > 0 && <HedgePairTable pairs={sfPairs} />}
      {ffPairs.length > 0 && <FuturesPairTable pairs={ffPairs} />}
    </div>
  )
}
