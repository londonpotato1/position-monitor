// Package services - PortfolioService: 포트폴리오 대시보드 집계 서비스
package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/londonpotato1/position-monitor/internal/db"
	"github.com/londonpotato1/position-monitor/internal/exchange"

	"github.com/rs/zerolog"
)

const (
	// fallbackUSDTKRW USDT/KRW 실시간 조회 실패 시 사용되는 기본값
	fallbackUSDTKRW = 1400.0
)

const createSnapshotSchema = `
CREATE TABLE IF NOT EXISTS daily_snapshots (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    date             TEXT NOT NULL UNIQUE,
    total_assets_usd REAL NOT NULL,
    total_pnl        REAL,
    spot_usd         REAL,
    futures_usd      REAL,
    exchange_details TEXT,
    created_at       TEXT DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_daily_snapshots_date ON daily_snapshots(date);
`

// ExchangeSummary 거래소별 요약
type ExchangeSummary struct {
	Exchange      string  `json:"exchange"`
	SpotValueUSD  float64 `json:"spotValueUSD"`
	FuturesMargin float64 `json:"futuresMargin"`
	AdditionalUSD float64 `json:"additionalUSD"`
	UnrealizedPnL float64 `json:"unrealizedPnl"`
	TotalUSD      float64 `json:"totalUSD"`
}

// PortfolioSummary 포트폴리오 전체 요약
type PortfolioSummary struct {
	TotalAssetsUSD      float64           `json:"totalAssetsUSD"`
	TotalSpotUSD        float64           `json:"totalSpotUSD"`
	TotalFuturesUSD     float64           `json:"totalFuturesUSD"`
	TotalAdditionalUSD  float64           `json:"totalAdditionalUSD"`
	TotalUnrealizedPnL  float64           `json:"totalUnrealizedPnl"`
	MatchRate           float64           `json:"matchRate"`
	HedgedPairCount     int               `json:"hedgedPairCount"`
	UnmatchedCount      int               `json:"unmatchedCount"`
	Exchanges           []ExchangeSummary `json:"exchanges"`
	UpdatedAt           string            `json:"updatedAt"`
}

// DailySnapshot 일별 자산 스냅샷
type DailySnapshot struct {
	Date           string  `json:"date"`
	TotalAssetsUSD float64 `json:"totalAssetsUSD"`
	TotalPnL       float64 `json:"totalPnl"`
}

// PortfolioService 포트폴리오 집계 서비스
type PortfolioService struct {
	exchangeMgr *ExchangeManager
	posCache    *PositionCache
	db          *sql.DB
	logger      zerolog.Logger
}

// NewPortfolioService 새 PortfolioService 생성
func NewPortfolioService(exchangeMgr *ExchangeManager, posCache *PositionCache, dbPath string, logger zerolog.Logger) (*PortfolioService, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("DB 디렉터리 생성 실패: %w", err)
	}

	sqlDB, err := db.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("portfolio DB 열기 실패: %w", err)
	}

	if _, err := sqlDB.Exec(createSnapshotSchema); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("snapshot 스키마 생성 실패: %w", err)
	}

	svc := &PortfolioService{
		exchangeMgr: exchangeMgr,
		posCache:    posCache,
		db:          sqlDB,
		logger:      logger.With().Str("component", "portfolio_service").Logger(),
	}
	return svc, nil
}

// getUSDTKRWRate 실시간 USDT/KRW 환율 조회 (업비트 → 빗썸 순서로 시도)
func (s *PortfolioService) getUSDTKRWRate(ctx context.Context) float64 {
	// 업비트에서 USDT/KRW 가격 조회 시도
	if upbitEx, ok := s.exchangeMgr.GetDomesticExchange("upbit"); ok {
		priceCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		ticker, err := upbitEx.GetTicker(priceCtx, "USDT")
		cancel()
		if err == nil && ticker != nil && ticker.Price > 0 {
			return ticker.Price
		}
	}
	// 빗썸에서 USDT/KRW 가격 조회 시도
	if bithumbEx, ok := s.exchangeMgr.GetDomesticExchange("bithumb"); ok {
		priceCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		ticker, err := bithumbEx.GetTicker(priceCtx, "USDT")
		cancel()
		if err == nil && ticker != nil && ticker.Price > 0 {
			return ticker.Price
		}
	}
	return fallbackUSDTKRW
}

// GetSummary 현재 포트폴리오 요약 반환
func (s *PortfolioService) GetSummary(ctx context.Context) (*PortfolioSummary, error) {
	summary := &PortfolioSummary{
		UpdatedAt: time.Now().Format(time.RFC3339),
	}

	exchangeSummaries := make(map[string]*ExchangeSummary)

	// 실시간 USDT/KRW 환율 조회
	usdtKRWRate := s.getUSDTKRWRate(ctx)

	// 1. 국내 거래소: 코인 수량 × 현재가(KRW) / usdtKRWRate
	for name, domEx := range s.exchangeMgr.GetAllDomesticExchanges() {
		if !domEx.IsConnected() {
			continue
		}
		balances, err := domEx.GetAllBalances(ctx)
		if err != nil {
			s.logger.Warn().Str("exchange", name).Err(err).Msg("국내 잔고 조회 실패")
			continue
		}

		spotValueUSD := 0.0
		for coin, bal := range balances {
			if bal.Total <= 0 {
				continue
			}
			if coin == "KRW" {
				spotValueUSD += bal.Total / usdtKRWRate
				continue
			}
			// 코인 → KRW 가격 조회
			priceCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			ticker, err := domEx.GetTicker(priceCtx, coin)
			cancel()
			if err != nil || ticker == nil || ticker.Price <= 0 {
				s.logger.Warn().Str("exchange", name).Str("coin", coin).Msg("가격 조회 실패, 스킵")
				continue
			}
			spotValueUSD += bal.Total * ticker.Price / usdtKRWRate
		}

		es := &ExchangeSummary{
			Exchange:     name,
			SpotValueUSD: spotValueUSD,
			TotalUSD:     spotValueUSD,
		}
		exchangeSummaries[name] = es
		summary.TotalSpotUSD += spotValueUSD
	}

	// 2. 해외 거래소: 현물 + 선물 잔고
	for name, ovsEx := range s.exchangeMgr.GetAllOverseasExchanges() {
		if !ovsEx.IsConnected() {
			continue
		}

		spotValueUSD := 0.0
		futuresMargin := 0.0

		// 현물 잔고
		allBals, err := ovsEx.GetAllBalances(ctx)
		if err != nil {
			s.logger.Warn().Str("exchange", name).Err(err).Msg("해외 현물 잔고 조회 실패")
		} else {
			stablecoins := map[string]bool{
				"USDT": true, "USDC": true, "BUSD": true, "TUSD": true,
				"FDUSD": true, "USD1": true, "USDE": true, "DAI": true, "USDP": true,
			}
			for coin, bal := range allBals {
				if bal.Total <= 0 {
					continue
				}
				if stablecoins[coin] {
					spotValueUSD += bal.Total
					continue
				}
				priceCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				ticker, err := ovsEx.GetSpotTicker(priceCtx, coin)
				cancel()
				if err != nil || ticker == nil || ticker.Price <= 0 {
					continue
				}
				spotValueUSD += bal.Total * ticker.Price
			}
		}

		// 선물 잔고
		// UTA(Unified Trading Account) 거래소는 GetAllBalances()에 선물 잔고 포함
		// 별도 합산하면 이중 계산됨
		if !ovsEx.IsUnifiedAccount() {
			futuresBal, err := ovsEx.GetFuturesBalance(ctx)
			if err != nil {
				s.logger.Warn().Str("exchange", name).Err(err).Msg("해외 선물 잔고 조회 실패")
			} else if futuresBal != nil {
				futuresMargin = futuresBal.Total
			}
		}

		// 추가 지갑 잔고 (earn, margin, funding 등)
		var additionalUSD float64
		if mwp, ok := ovsEx.(exchange.MultiWalletProvider); ok {
			addBals, err := mwp.GetAdditionalBalances(ctx)
			if err != nil {
				s.logger.Warn().Str("exchange", name).Err(err).Msg("추가 지갑 잔고 조회 실패")
			} else {
				for _, v := range addBals {
					additionalUSD += v
				}
			}
		}

		es := &ExchangeSummary{
			Exchange:      name,
			SpotValueUSD:  spotValueUSD,
			FuturesMargin: futuresMargin,
			AdditionalUSD: additionalUSD,
			TotalUSD:      spotValueUSD + futuresMargin + additionalUSD,
		}
		exchangeSummaries[name] = es
		summary.TotalSpotUSD += spotValueUSD
		summary.TotalFuturesUSD += futuresMargin
		summary.TotalAdditionalUSD += additionalUSD
	}

	// 3. 미실현 PnL: posCache에서 집계
	if s.posCache != nil {
		posResp := s.posCache.Get(ctx)

		totalUnrealized := 0.0
		for _, pair := range posResp.Pairs {
			totalUnrealized += pair.FuturesPnL
		}
		for _, leg := range posResp.Unmatched {
			totalUnrealized += leg.PnL
		}
		summary.TotalUnrealizedPnL = totalUnrealized

		// 4. 매칭률 계산
		hedgedCount := len(posResp.Pairs)
		unmatchedCount := len(posResp.Unmatched)
		summary.HedgedPairCount = hedgedCount
		summary.UnmatchedCount = unmatchedCount

		total := hedgedCount + unmatchedCount
		if total > 0 {
			summary.MatchRate = float64(hedgedCount) / float64(total) * 100.0
		}
	}

	// 5. exchange_details를 미실현 PnL 포함해서 완성
	// posCache의 exchange별 PnL 매핑
	if s.posCache != nil {
		posResp := s.posCache.Get(ctx)
		exchangePnL := make(map[string]float64)
		for _, pair := range posResp.Pairs {
			if pair.FuturesLeg != nil {
				exchangePnL[pair.FuturesLeg.Exchange] += pair.FuturesPnL
			}
		}
		for _, leg := range posResp.Unmatched {
			if leg.MarketType == "futures" {
				exchangePnL[leg.Exchange] += leg.PnL
			}
		}
		for name, pnl := range exchangePnL {
			if es, ok := exchangeSummaries[name]; ok {
				es.UnrealizedPnL = pnl
			}
		}
	}

	// 6. Exchanges 슬라이스 구성
	for _, es := range exchangeSummaries {
		summary.Exchanges = append(summary.Exchanges, *es)
	}

	summary.TotalAssetsUSD = summary.TotalSpotUSD + summary.TotalFuturesUSD + summary.TotalAdditionalUSD + summary.TotalUnrealizedPnL

	return summary, nil
}

// SaveDailySnapshot 오늘 날짜의 포트폴리오 스냅샷 저장
func (s *PortfolioService) SaveDailySnapshot(ctx context.Context) error {
	summary, err := s.GetSummary(ctx)
	if err != nil {
		return fmt.Errorf("summary 조회 실패: %w", err)
	}

	date := time.Now().UTC().Format("2006-01-02")

	detailsJSON, _ := json.Marshal(summary.Exchanges)

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO daily_snapshots (date, total_assets_usd, total_pnl, spot_usd, futures_usd, exchange_details)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(date) DO UPDATE SET
			total_assets_usd = excluded.total_assets_usd,
			total_pnl        = excluded.total_pnl,
			spot_usd         = excluded.spot_usd,
			futures_usd      = excluded.futures_usd,
			exchange_details = excluded.exchange_details,
			created_at       = datetime('now')
	`,
		date,
		summary.TotalAssetsUSD,
		summary.TotalUnrealizedPnL,
		summary.TotalSpotUSD,
		summary.TotalFuturesUSD,
		string(detailsJSON),
	)
	if err != nil {
		return fmt.Errorf("스냅샷 저장 실패: %w", err)
	}

	s.logger.Info().Str("date", date).Float64("totalUSD", summary.TotalAssetsUSD).Msg("일별 스냅샷 저장")
	return nil
}

// GetSnapshots 최근 N일 스냅샷 조회
func (s *PortfolioService) GetSnapshots(days int) ([]DailySnapshot, error) {
	if days <= 0 {
		days = 30
	}

	rows, err := s.db.Query(`
		SELECT date, total_assets_usd, total_pnl
		FROM daily_snapshots
		ORDER BY date DESC
		LIMIT ?
	`, days)
	if err != nil {
		return nil, fmt.Errorf("스냅샷 조회 실패: %w", err)
	}
	defer rows.Close()

	var snapshots []DailySnapshot
	for rows.Next() {
		var snap DailySnapshot
		var totalPnL sql.NullFloat64
		if err := rows.Scan(&snap.Date, &snap.TotalAssetsUSD, &totalPnL); err != nil {
			return nil, fmt.Errorf("스냅샷 스캔 실패: %w", err)
		}
		if totalPnL.Valid {
			snap.TotalPnL = totalPnL.Float64
		}
		snapshots = append(snapshots, snap)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("스냅샷 rows 에러: %w", err)
	}

	if snapshots == nil {
		snapshots = []DailySnapshot{}
	}
	return snapshots, nil
}

// StartDailySnapshot 매일 00:00 UTC에 스냅샷 자동 저장
func (s *PortfolioService) StartDailySnapshot(ctx context.Context) {
	go func() {
		for {
			now := time.Now().UTC()
			next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
			wait := time.Until(next)

			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}

			if err := s.SaveDailySnapshot(ctx); err != nil {
				s.logger.Error().Err(err).Msg("자동 일별 스냅샷 저장 실패")
			}
		}
	}()

	s.logger.Info().Msg("일별 스냅샷 자동 저장 시작")
}

// Close DB 연결 종료
func (s *PortfolioService) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
