// Package services - SnapshotService: 5분 간격 잔고 스냅샷 + 스무딩
package services

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/londonpotato1/position-monitor/internal/db"

	"github.com/rs/zerolog"
)

const createBalanceSnapshotSchema = `
CREATE TABLE IF NOT EXISTS balance_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp TEXT NOT NULL,
    total_assets_usd REAL NOT NULL,
    total_krw REAL,
    btc_price REAL,
    usdt_krw_rate REAL,
    spot_usd REAL,
    futures_usd REAL,
    unrealized_pnl REAL,
    created_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_snapshots_timestamp ON balance_snapshots(timestamp);

CREATE TABLE IF NOT EXISTS balance_snapshot_details (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    snapshot_id INTEGER NOT NULL,
    exchange TEXT NOT NULL,
    wallet_type TEXT NOT NULL,
    asset TEXT NOT NULL,
    amount REAL NOT NULL,
    usd_value REAL NOT NULL,
    FOREIGN KEY (snapshot_id) REFERENCES balance_snapshots(id)
);
CREATE INDEX IF NOT EXISTS idx_snapshot_details_sid ON balance_snapshot_details(snapshot_id);
`

// BalanceHistoryPoint 잔고 히스토리 포인트
type BalanceHistoryPoint struct {
	Timestamp string  `json:"timestamp"`
	TotalUSD  float64 `json:"totalUSD"`
	TotalKRW  float64 `json:"totalKRW"`
	BTCPrice  float64 `json:"btcPrice"`
}

// Change24h 24시간 변동 정보
type Change24h struct {
	ChangeUSD float64 `json:"changeUSD"`
	ChangePct float64 `json:"changePct"`
	PrevTotal float64 `json:"prevTotal"`
	CurrTotal float64 `json:"currTotal"`
}

// CumulativeReturn 누적 수익률
type CumulativeReturn struct {
	ReturnUSD         float64 `json:"returnUSD"`
	ReturnPct         float64 `json:"returnPct"`
	FirstSnapshotDate string  `json:"firstSnapshotDate"`
	FirstSnapshotUSD  float64 `json:"firstSnapshotUSD"`
}

// SnapshotService 5분 간격 잔고 스냅샷 서비스
type SnapshotService struct {
	portfolioSvc *PortfolioService
	db           *sql.DB
	logger       zerolog.Logger
	stopCh       chan struct{}
}

// NewSnapshotService 새 SnapshotService 생성
func NewSnapshotService(portfolioSvc *PortfolioService, dbPath string, logger zerolog.Logger) (*SnapshotService, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("snapshots DB 디렉터리 생성 실패: %w", err)
	}

	sqlDB, err := db.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("snapshots DB 열기 실패: %w", err)
	}

	if _, err := sqlDB.Exec(createBalanceSnapshotSchema); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("snapshots 스키마 생성 실패: %w", err)
	}

	svc := &SnapshotService{
		portfolioSvc: portfolioSvc,
		db:           sqlDB,
		logger:       logger.With().Str("component", "snapshot_service").Logger(),
		stopCh:       make(chan struct{}),
	}
	return svc, nil
}

// TakeSnapshot 현재 포트폴리오 스냅샷 저장
func (s *SnapshotService) TakeSnapshot(ctx context.Context) error {
	summary, err := s.portfolioSvc.GetSummary(ctx)
	if err != nil {
		return fmt.Errorf("포트폴리오 요약 조회 실패: %w", err)
	}

	usdtKRWRate := s.portfolioSvc.getUSDTKRWRate(ctx)
	totalKRW := summary.TotalAssetsUSD * usdtKRWRate
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("트랜잭션 시작 실패: %w", err)
	}
	defer tx.Rollback()

	// 1. 메인 스냅샷 삽입
	result, err := tx.ExecContext(ctx, `
		INSERT INTO balance_snapshots (timestamp, total_assets_usd, total_krw, btc_price, usdt_krw_rate, spot_usd, futures_usd, unrealized_pnl)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		now,
		summary.TotalAssetsUSD,
		totalKRW,
		0.0, // BTC 가격은 Phase C에서 채움
		usdtKRWRate,
		summary.TotalSpotUSD,
		summary.TotalFuturesUSD,
		summary.TotalUnrealizedPnL,
	)
	if err != nil {
		return fmt.Errorf("스냅샷 삽입 실패: %w", err)
	}

	snapshotID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("스냅샷 ID 조회 실패: %w", err)
	}

	// 2. 거래소별 상세 저장
	var detailErrors int
	for _, es := range summary.Exchanges {
		if es.SpotValueUSD > 0.01 {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO balance_snapshot_details (snapshot_id, exchange, wallet_type, asset, amount, usd_value)
				VALUES (?, ?, ?, ?, ?, ?)
			`, snapshotID, es.Exchange, "spot", "TOTAL", es.SpotValueUSD, es.SpotValueUSD); err != nil {
				detailErrors++
				s.logger.Warn().Str("exchange", es.Exchange).Err(err).Msg("스냅샷 상세(spot) 삽입 실패")
			}
		}
		if es.FuturesMargin > 0.01 {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO balance_snapshot_details (snapshot_id, exchange, wallet_type, asset, amount, usd_value)
				VALUES (?, ?, ?, ?, ?, ?)
			`, snapshotID, es.Exchange, "futures", "TOTAL", es.FuturesMargin, es.FuturesMargin); err != nil {
				detailErrors++
				s.logger.Warn().Str("exchange", es.Exchange).Err(err).Msg("스냅샷 상세(futures) 삽입 실패")
			}
		}
		if es.UnrealizedPnL != 0 {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO balance_snapshot_details (snapshot_id, exchange, wallet_type, asset, amount, usd_value)
				VALUES (?, ?, ?, ?, ?, ?)
			`, snapshotID, es.Exchange, "unrealized_pnl", "PNL", es.UnrealizedPnL, es.UnrealizedPnL); err != nil {
				detailErrors++
				s.logger.Warn().Str("exchange", es.Exchange).Err(err).Msg("스냅샷 상세(pnl) 삽입 실패")
			}
		}
	}
	if detailErrors > 0 {
		s.logger.Error().Int("count", detailErrors).Msg("스냅샷 상세 삽입 에러 발생, 트랜잭션 롤백")
		return fmt.Errorf("스냅샷 상세 삽입 실패: %d건 에러", detailErrors)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("트랜잭션 커밋 실패: %w", err)
	}

	s.logger.Info().
		Float64("totalUSD", summary.TotalAssetsUSD).
		Float64("usdtKRW", usdtKRWRate).
		Int("exchanges", len(summary.Exchanges)).
		Msg("스냅샷 저장 완료")
	return nil
}

// Start5MinLoop 5분 간격 스냅샷 goroutine 시작
func (s *SnapshotService) Start5MinLoop(ctx context.Context) {
	// 시작 시 즉시 1회 스냅샷
	if err := s.TakeSnapshot(ctx); err != nil {
		s.logger.Warn().Err(err).Msg("초기 스냅샷 실패")
	}

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	s.logger.Info().Msg("5분 스냅샷 루프 시작")

	for {
		select {
		case <-ticker.C:
			if err := s.TakeSnapshot(ctx); err != nil {
				s.logger.Warn().Err(err).Msg("주기적 스냅샷 실패")
			}
		case <-s.stopCh:
			s.logger.Info().Msg("5분 스냅샷 루프 종료")
			return
		case <-ctx.Done():
			s.logger.Info().Msg("5분 스냅샷 루프 종료 (ctx)")
			return
		}
	}
}

// Stop 스냅샷 루프 중지
func (s *SnapshotService) Stop() {
	select {
	case <-s.stopCh:
		// 이미 닫힘
	default:
		close(s.stopCh)
	}
}

// TakeManualSnapshot 수동 스냅샷
func (s *SnapshotService) TakeManualSnapshot(ctx context.Context) error {
	return s.TakeSnapshot(ctx)
}

// GetHistory 잔고 히스토리 조회 (스무딩 적용)
func (s *SnapshotService) GetHistory(period string) ([]BalanceHistoryPoint, error) {
	periodDays := map[string]int{
		"7d":  7,
		"30d": 30,
		"90d": 90,
	}

	var rows *sql.Rows
	var err error

	if period == "all" {
		rows, err = s.db.Query(`
			SELECT timestamp, total_assets_usd, total_krw, btc_price
			FROM balance_snapshots
			ORDER BY timestamp ASC
		`)
	} else {
		days, ok := periodDays[period]
		if !ok {
			days = 7
		}
		since := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)
		rows, err = s.db.Query(`
			SELECT timestamp, total_assets_usd, total_krw, btc_price
			FROM balance_snapshots
			WHERE timestamp >= ?
			ORDER BY timestamp ASC
		`, since)
	}
	if err != nil {
		return nil, fmt.Errorf("히스토리 조회 실패: %w", err)
	}
	defer rows.Close()

	var points []BalanceHistoryPoint
	for rows.Next() {
		var p BalanceHistoryPoint
		var totalKRW, btcPrice sql.NullFloat64
		if err := rows.Scan(&p.Timestamp, &p.TotalUSD, &totalKRW, &btcPrice); err != nil {
			return nil, fmt.Errorf("히스토리 스캔 실패: %w", err)
		}
		if totalKRW.Valid {
			p.TotalKRW = totalKRW.Float64
		}
		if btcPrice.Valid {
			p.BTCPrice = btcPrice.Float64
		}
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("히스토리 rows 에러: %w", err)
	}

	if points == nil {
		points = []BalanceHistoryPoint{}
	}

	// 스무딩 적용 (읽기 시점)
	if len(points) >= 3 {
		points = smoothTransitValleys(points, 24, 0.03)
		points = smoothOutliers(points, 0.10)
	}

	return points, nil
}

// GetChange24h 24시간 전 스냅샷 대비 변동
func (s *SnapshotService) GetChange24h(ctx context.Context) (*Change24h, error) {
	// 현재 포트폴리오 요약
	summary, err := s.portfolioSvc.GetSummary(ctx)
	if err != nil {
		return nil, fmt.Errorf("현재 포트폴리오 조회 실패: %w", err)
	}
	currTotal := summary.TotalAssetsUSD

	// 24시간 전 스냅샷
	since := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	var prevTotal float64
	err = s.db.QueryRow(`
		SELECT total_assets_usd
		FROM balance_snapshots
		WHERE timestamp <= ?
		ORDER BY timestamp DESC
		LIMIT 1
	`, since).Scan(&prevTotal)
	if err != nil {
		if err == sql.ErrNoRows {
			// 24시간 전 스냅샷이 없으면 가장 오래된 스냅샷 사용
			err = s.db.QueryRow(`
				SELECT total_assets_usd
				FROM balance_snapshots
				ORDER BY timestamp ASC
				LIMIT 1
			`).Scan(&prevTotal)
			if err != nil {
				return &Change24h{CurrTotal: currTotal}, nil
			}
		} else {
			return nil, fmt.Errorf("24h 전 스냅샷 조회 실패: %w", err)
		}
	}

	changeUSD := currTotal - prevTotal
	changePct := 0.0
	if prevTotal > 0 {
		changePct = changeUSD / prevTotal * 100.0
	}

	return &Change24h{
		ChangeUSD: changeUSD,
		ChangePct: changePct,
		PrevTotal: prevTotal,
		CurrTotal: currTotal,
	}, nil
}

// GetCumulativeReturn 첫 스냅샷 대비 누적 수익률
func (s *SnapshotService) GetCumulativeReturn() (*CumulativeReturn, error) {
	var firstDate string
	var firstUSD float64
	err := s.db.QueryRow(`
		SELECT timestamp, total_assets_usd
		FROM balance_snapshots
		ORDER BY timestamp ASC
		LIMIT 1
	`).Scan(&firstDate, &firstUSD)
	if err != nil {
		if err == sql.ErrNoRows {
			return &CumulativeReturn{}, nil
		}
		return nil, fmt.Errorf("첫 스냅샷 조회 실패: %w", err)
	}

	var latestUSD float64
	err = s.db.QueryRow(`
		SELECT total_assets_usd
		FROM balance_snapshots
		ORDER BY timestamp DESC
		LIMIT 1
	`).Scan(&latestUSD)
	if err != nil {
		return nil, fmt.Errorf("최신 스냅샷 조회 실패: %w", err)
	}

	returnUSD := latestUSD - firstUSD
	returnPct := 0.0
	if firstUSD > 0 {
		returnPct = returnUSD / firstUSD * 100.0
	}

	return &CumulativeReturn{
		ReturnUSD:         returnUSD,
		ReturnPct:         returnPct,
		FirstSnapshotDate: firstDate,
		FirstSnapshotUSD:  firstUSD,
	}, nil
}

// AssetDistributionItem 자산별 분배 항목
type AssetDistributionItem struct {
	Asset    string  `json:"asset"`
	Amount   float64 `json:"amount"`
	USDValue float64 `json:"usdValue"`
	Pct      float64 `json:"pct"`
}

// GetLatestAssetDistribution 최신 스냅샷의 자산별 분배 조회
func (s *SnapshotService) GetLatestAssetDistribution() ([]AssetDistributionItem, error) {
	// 최신 snapshot_id 조회
	var snapshotID int64
	err := s.db.QueryRow(`
		SELECT id FROM balance_snapshots
		ORDER BY timestamp DESC
		LIMIT 1
	`).Scan(&snapshotID)
	if err != nil {
		if err == sql.ErrNoRows {
			return []AssetDistributionItem{}, nil
		}
		return nil, fmt.Errorf("최신 스냅샷 ID 조회 실패: %w", err)
	}

	rows, err := s.db.Query(`
		SELECT asset, SUM(amount) AS amount, SUM(usd_value) AS usd_value
		FROM balance_snapshot_details
		WHERE snapshot_id = ?
		GROUP BY asset
		ORDER BY usd_value DESC
	`, snapshotID)
	if err != nil {
		return nil, fmt.Errorf("자산 분배 조회 실패: %w", err)
	}
	defer rows.Close()

	var items []AssetDistributionItem
	var totalUSD float64
	for rows.Next() {
		var item AssetDistributionItem
		if err := rows.Scan(&item.Asset, &item.Amount, &item.USDValue); err != nil {
			return nil, fmt.Errorf("자산 분배 스캔 실패: %w", err)
		}
		totalUSD += item.USDValue
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("자산 분배 rows 에러: %w", err)
	}

	// 비율 계산
	if totalUSD > 0 {
		for i := range items {
			items[i].Pct = items[i].USDValue / totalUSD * 100.0
		}
	}

	if items == nil {
		items = []AssetDistributionItem{}
	}

	return items, nil
}

// GetDailySnapshots balance_snapshots에서 일별 마지막 스냅샷을 집계
func (s *SnapshotService) GetDailySnapshots(days int) ([]DailySnapshot, error) {
	if days <= 0 {
		days = 30
	}
	if days > 365 {
		days = 365
	}

	rows, err := s.db.Query(`
		SELECT DATE(timestamp) AS date,
		       total_assets_usd,
		       COALESCE(unrealized_pnl, 0)
		FROM balance_snapshots
		WHERE timestamp >= datetime('now', ?)
		  AND id IN (
		    SELECT MAX(id) FROM balance_snapshots
		    GROUP BY DATE(timestamp)
		  )
		ORDER BY date DESC
	`, fmt.Sprintf("-%d days", days))
	if err != nil {
		return nil, fmt.Errorf("일별 스냅샷 조회 실패: %w", err)
	}
	defer rows.Close()

	var result []DailySnapshot
	for rows.Next() {
		var ds DailySnapshot
		if err := rows.Scan(&ds.Date, &ds.TotalAssetsUSD, &ds.TotalPnL); err != nil {
			continue
		}
		result = append(result, ds)
	}
	if result == nil {
		result = []DailySnapshot{}
	}
	return result, nil
}

// Close DB 연결 종료
func (s *SnapshotService) Close() {
	s.Stop()
	if s.db != nil {
		s.db.Close()
	}
}

// ============ 스무딩 로직 (balance_dashboard_v2 history.py 참조) ============

// smoothOutliers 전후 값 대비 thresholdPct 이상 벗어난 이상치 제거
func smoothOutliers(points []BalanceHistoryPoint, thresholdPct float64) []BalanceHistoryPoint {
	if len(points) < 3 {
		return points
	}

	outlierSet := make(map[int]bool)

	// Pass 1: 단일 포인트 이상치 탐지
	for i := 1; i < len(points)-1; i++ {
		if isSpike(points[i-1].TotalUSD, points[i].TotalUSD, points[i+1].TotalUSD, thresholdPct) {
			outlierSet[i] = true
		}
		// 넓은 윈도우 체크 (i-2, i+2)
		if i >= 2 && i+2 < len(points) {
			if isSpike(points[i-2].TotalUSD, points[i].TotalUSD, points[i+2].TotalUSD, thresholdPct) {
				outlierSet[i] = true
			}
		}
	}

	// Pass 2: 다중 포인트 스파이크 (전체 median 대비)
	if len(points) >= 10 {
		values := make([]float64, len(points))
		for i, p := range points {
			values[i] = p.TotalUSD
		}
		sorted := make([]float64, len(values))
		copy(sorted, values)
		sort.Float64s(sorted)
		medianUSD := sorted[len(sorted)/2]

		i := 0
		for i < len(points) {
			if outlierSet[i] {
				i++
				continue
			}
			curr := points[i].TotalUSD
			if medianUSD > 0 {
				dev := math.Abs(curr-medianUSD) / medianUSD
				if dev > thresholdPct {
					spikeStart := i
					spikeEnd := i
					for j := i + 1; j < len(points); j++ {
						jdev := math.Abs(points[j].TotalUSD-medianUSD) / medianUSD
						if jdev > thresholdPct*0.5 {
							spikeEnd = j
						} else {
							break
						}
					}
					spikeLen := spikeEnd - spikeStart + 1
					maxLen := 12 // 최대 12포인트(1시간)까지만 이상치 제거
					if maxLen > len(points)/5 {
						maxLen = len(points) / 5
					}
					if maxLen < 3 {
						maxLen = 3
					}
					if spikeLen <= maxLen {
						for k := spikeStart; k <= spikeEnd; k++ {
							if k != 0 && k != len(points)-1 {
								outlierSet[k] = true
							}
						}
					}
					i = spikeEnd + 1
					continue
				}
			}
			i++
		}
	}

	result := make([]BalanceHistoryPoint, 0, len(points))
	for i, p := range points {
		if !outlierSet[i] {
			result = append(result, p)
		}
	}
	return result
}

// smoothTransitValleys 거래소 간 이체 시 임시 골짜기 보정
// 급락 후 window 내 회복하는 V형 valley를 전후 값 평균으로 대체
func smoothTransitValleys(points []BalanceHistoryPoint, window int, threshold float64) []BalanceHistoryPoint {
	if len(points) < 3 {
		return points
	}

	result := make([]BalanceHistoryPoint, len(points))
	copy(result, points)
	smoothed := make(map[int]bool)

	i := 1
	for i < len(result)-1 {
		if smoothed[i] {
			i++
			continue
		}

		prevUSD := result[i-1].TotalUSD
		currUSD := result[i].TotalUSD
		if prevUSD <= 0 {
			i++
			continue
		}

		dropPct := (prevUSD - currUSD) / prevUSD
		if dropPct < threshold {
			i++
			continue
		}

		// 급락 발견 → window 내 회복점 탐색
		recoveryIdx := -1
		endSearch := i + window + 1
		if endSearch > len(result) {
			endSearch = len(result)
		}
		for j := i + 1; j < endSearch; j++ {
			if result[j].TotalUSD >= prevUSD*0.97 { // 97% 이상 회복
				recoveryIdx = j
				break
			}
		}

		if recoveryIdx > 0 {
			// 회복된 valley → 선형 보간
			startUSD := result[i-1].TotalUSD
			startKRW := result[i-1].TotalKRW
			endUSD := result[recoveryIdx].TotalUSD
			endKRW := result[recoveryIdx].TotalKRW
			span := float64(recoveryIdx - (i - 1))

			for k := i; k < recoveryIdx; k++ {
				t := float64(k-(i-1)) / span
				result[k] = BalanceHistoryPoint{
					Timestamp: result[k].Timestamp,
					TotalUSD:  math.Round((startUSD+(endUSD-startUSD)*t)*100) / 100,
					TotalKRW:  math.Round((startKRW+(endKRW-startKRW)*t)*100) / 100,
					BTCPrice:  result[k].BTCPrice,
				}
				smoothed[k] = true
			}
			i = recoveryIdx + 1
		} else {
			// 미회복 = window(2시간) 내 원래 수준으로 복구 안 됨 → 실제 자산 변동으로 간주.
			// 기존 로직은 이후 24포인트를 이전 값으로 평탄화했으나, 이는 실제 포지션 청산·
			// 출금·손실 반영을 가로막아 그래프가 영구 평탄 고점으로 고착되는 버그 원인이었음.
			// 이제 원본값 그대로 유지 (정직한 반영). 이체 중 일시 valley는 이미 위 if 블록이 처리.
			i++
		}
	}

	return result
}

// isSpike 단일 값이 양쪽 이웃 대비 스파이크인지 판단
func isSpike(prev, curr, next, threshold float64) bool {
	if prev <= 0 || next <= 0 {
		return false
	}
	devPrev := math.Abs(curr-prev) / prev
	devNext := math.Abs(curr-next) / next
	return devPrev > threshold && devNext > threshold
}
