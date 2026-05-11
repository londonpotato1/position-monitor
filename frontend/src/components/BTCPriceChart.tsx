// ============================================================
// BTCPriceChart - BTC 가격 캔들스틱 차트
// lightweight-charts v5, autoSize 사용 안 함 (fontsize 버그 우회)
// ResizeObserver로 수동 리사이즈
// ============================================================

import React, { useCallback, useEffect, useRef, useState } from 'react'
import {
  createChart,
  CandlestickSeries,
  CrosshairMode,
  type IChartApi,
  type ISeriesApi,
  type CandlestickData,
} from 'lightweight-charts'
import type { KlinePoint } from '../types'
import { GetBTCKlines } from '../../wailsjs/go/main/App'

type Interval = '15m' | '4h' | '1D' | '1W'

const INTERVALS: { key: Interval; label: string; limit: number }[] = [
  { key: '15m', label: '15m', limit: 200 },
  { key: '4h',  label: '4H',  limit: 200 },
  { key: '1D',  label: '1D',  limit: 180 },
  { key: '1W',  label: '1W',  limit: 104 },
]

const UP_COLOR = '#3fb950'
const DOWN_COLOR = '#f85149'

export default function BTCPriceChart() {
  const containerRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<IChartApi | null>(null)
  const seriesRef = useRef<ISeriesApi<'Candlestick'> | null>(null)
  const [interval, setInterval] = useState<Interval>('4h')
  const [loading, setLoading] = useState(false)
  const [isEmpty, setIsEmpty] = useState(false)
  const [collapsed, setCollapsed] = useState(false)

  // 차트 초기화
  useEffect(() => {
    const container = containerRef.current
    if (!container) return

    const w = container.clientWidth || 600
    const h = 300

    const chart = createChart(container, {
      width: w,
      height: h,
      layout: {
        background: { color: '#0d1117' },
        textColor: '#8b949e',
        fontSize: 11,
        fontFamily: "-apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif",
      },
      grid: {
        vertLines: { color: '#21262d' },
        horzLines: { color: '#21262d' },
      },
      crosshair: {
        mode: CrosshairMode.Normal,
        vertLine: { color: '#484f58', labelBackgroundColor: '#21262d' },
        horzLine: { color: '#484f58', labelBackgroundColor: '#21262d' },
      },
      rightPriceScale: {
        borderColor: '#21262d',
      },
      timeScale: {
        borderColor: '#21262d',
        timeVisible: true,
        secondsVisible: false,
      },
    })

    const series = chart.addSeries(CandlestickSeries, {
      upColor: UP_COLOR,
      downColor: DOWN_COLOR,
      borderUpColor: UP_COLOR,
      borderDownColor: DOWN_COLOR,
      wickUpColor: UP_COLOR,
      wickDownColor: DOWN_COLOR,
    })

    chartRef.current = chart
    seriesRef.current = series

    // ResizeObserver로 수동 크기 조정 (autoSize 사용 안 함)
    const observer = new ResizeObserver(entries => {
      for (const entry of entries) {
        const { width } = entry.contentRect
        if (width > 0 && chartRef.current) {
          chartRef.current.applyOptions({ width })
        }
      }
    })
    observer.observe(container)

    return () => {
      observer.disconnect()
      chart.remove()
      chartRef.current = null
      seriesRef.current = null
    }
  }, [])

  const fetchKlines = useCallback(async (iv: Interval) => {
    setLoading(true)
    try {
      const cfg = INTERVALS.find(x => x.key === iv)!
      const data = await GetBTCKlines(iv, cfg.limit)
      if (!data || !Array.isArray(data) || (data as unknown[]).length === 0) {
        setIsEmpty(true)
        seriesRef.current?.setData([])
        return
      }
      setIsEmpty(false)
      const klines = data as KlinePoint[]
      const candles: CandlestickData[] = klines
        .filter(k => k.timestamp > 0)
        .map(k => ({
          time: Math.floor(k.timestamp / 1000) as unknown as CandlestickData['time'],
          open: k.open,
          high: k.high,
          low: k.low,
          close: k.close,
        }))
        .sort((a, b) => (a.time as number) - (b.time as number))

      seriesRef.current?.setData(candles)
      chartRef.current?.timeScale().fitContent()
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (!collapsed) {
      fetchKlines(interval)
    }
  }, [interval, collapsed, fetchKlines])

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
            ₿ 비트코인 가격 차트
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
        {/* 인터벌 버튼 */}
        <div className="flex gap-0.5">
          {INTERVALS.map(({ key, label }) => (
            <button
              key={key}
              onClick={() => setInterval(key)}
              className="text-xs px-2 py-0.5 rounded transition-colors"
              style={{
                backgroundColor: interval === key ? '#58a6ff22' : 'transparent',
                color: interval === key ? '#58a6ff' : '#768390',
                border: `1px solid ${interval === key ? '#58a6ff44' : 'transparent'}`,
                cursor: 'pointer',
              }}
            >
              {label}
            </button>
          ))}
        </div>
      </div>

      {/* 차트 영역 */}
      {!collapsed && (
        <div
          style={{
            backgroundColor: '#0d1117',
            border: '1px solid #30363d',
            borderTop: 'none',
            borderBottomLeftRadius: 4,
            borderBottomRightRadius: 4,
            height: 300,
            position: 'relative',
          }}
        >
          {isEmpty && (
            <div
              style={{
                position: 'absolute',
                inset: 0,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                zIndex: 1,
                pointerEvents: 'none',
              }}
            >
              <span className="text-xs" style={{ color: '#484f58' }}>데이터 없음</span>
            </div>
          )}
          <div ref={containerRef} style={{ width: '100%', height: '100%' }} />
        </div>
      )}
    </section>
  )
}
