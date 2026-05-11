package services

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/londonpotato1/position-monitor/internal/config"
	"github.com/londonpotato1/position-monitor/internal/exchange"
	"github.com/londonpotato1/position-monitor/internal/exchange/binance"
	"github.com/londonpotato1/position-monitor/internal/exchange/bitget"
	"github.com/londonpotato1/position-monitor/internal/exchange/bithumb"
	"github.com/londonpotato1/position-monitor/internal/exchange/bybit"
	"github.com/londonpotato1/position-monitor/internal/exchange/gate"
	"github.com/londonpotato1/position-monitor/internal/exchange/htx"
	"github.com/londonpotato1/position-monitor/internal/exchange/hyperliquid"
	"github.com/londonpotato1/position-monitor/internal/exchange/kucoin"
	"github.com/londonpotato1/position-monitor/internal/exchange/lighter"
	"github.com/londonpotato1/position-monitor/internal/exchange/mexc"
	"github.com/londonpotato1/position-monitor/internal/exchange/okx"
	"github.com/londonpotato1/position-monitor/internal/exchange/upbit"

	"github.com/rs/zerolog"
)

// ExchangeBalance 거래소별 잔액 정보
type ExchangeBalance struct {
	Exchange        string
	DisplayName     string
	SpotTotal       float64
	FuturesTotal    float64
	FuturesFree     float64 // 선물 주문 가능 잔고 (Free)
	SpotBalances    map[string]*exchange.Balance
	FuturesBalances map[string]*exchange.Balance
	Connected       bool
}

// ExchangeManager 거래소 관리자
type ExchangeManager struct {
	domesticExchanges map[string]exchange.DomesticExchange
	overseasExchanges map[string]exchange.OverseasExchange
	balances          map[string]*ExchangeBalance
	mu                sync.RWMutex
	initialized       bool
	logger            zerolog.Logger
}

// NewExchangeManager 새 ExchangeManager 생성
func NewExchangeManager(logger zerolog.Logger) *ExchangeManager {
	return &ExchangeManager{
		domesticExchanges: make(map[string]exchange.DomesticExchange),
		overseasExchanges: make(map[string]exchange.OverseasExchange),
		balances:          make(map[string]*ExchangeBalance),
		logger:            logger.With().Str("component", "exchange_manager").Logger(),
	}
}

// Initialize 설정에서 거래소 초기화
func (em *ExchangeManager) Initialize(ctx context.Context, cfg *config.Config) error {
	em.mu.Lock()
	defer em.mu.Unlock()

	successCount := 0

	// 국내 거래소 초기화
	if cfg.Upbit.Enabled && cfg.Upbit.APIKey != "" {
		if err := em.connectDomestic(ctx, "upbit", cfg.Upbit.APIKey, cfg.Upbit.APISecret); err != nil {
			em.logger.Error().Err(err).Msg("upbit 연결 실패")
		} else {
			successCount++
		}
	}

	if cfg.Bithumb.Enabled && cfg.Bithumb.APIKey != "" {
		if err := em.connectDomestic(ctx, "bithumb", cfg.Bithumb.APIKey, cfg.Bithumb.APISecret); err != nil {
			em.logger.Error().Err(err).Msg("bithumb 연결 실패")
		} else {
			successCount++
		}
	}

	// 해외 거래소 초기화
	for name, exCfg := range cfg.Exchanges {
		if !exCfg.Enabled || exCfg.APIKey == "" {
			continue
		}

		if err := em.connectOverseas(ctx, name, exCfg.APIKey, exCfg.APISecret, exCfg.Passphrase, exCfg.Ed25519PrivateKey, exCfg.WSAPIKey); err != nil {
			em.logger.Error().Err(err).Str("exchange", name).Msg("거래소 연결 실패")
		} else {
			successCount++
		}
	}

	em.initialized = true
	em.logger.Info().Int("count", successCount).Msg("거래소 초기화 완료")

	return nil
}

// connectDomestic 국내 거래소 연결 (내부 사용)
func (em *ExchangeManager) connectDomestic(ctx context.Context, name, apiKey, apiSecret string) error {
	var adapter exchange.DomesticExchange

	switch name {
	case "upbit":
		adapter = upbit.NewAdapter(apiKey, apiSecret)
	case "bithumb":
		adapter = bithumb.NewAdapter(apiKey, apiSecret)
	default:
		return fmt.Errorf("unknown domestic exchange: %s", name)
	}

	if err := adapter.Connect(ctx); err != nil {
		return fmt.Errorf("%s 연결 실패: %w", name, err)
	}

	em.domesticExchanges[name] = adapter
	em.logger.Info().Str("exchange", name).Msg("국내 거래소 연결 성공")
	return nil
}

// ReconnectDomestic 국내 거래소 재연결 (IP 변경 등으로 연결이 끊긴 경우)
func (em *ExchangeManager) ReconnectDomestic(ctx context.Context, name, apiKey, apiSecret string) error {
	em.mu.Lock()
	defer em.mu.Unlock()
	return em.connectDomestic(ctx, name, apiKey, apiSecret)
}

// ReconnectOverseas 해외 거래소 재연결
func (em *ExchangeManager) ReconnectOverseas(ctx context.Context, name, apiKey, apiSecret, passphrase, ed25519Key, wsAPIKey string) error {
	em.mu.Lock()
	defer em.mu.Unlock()
	return em.connectOverseas(ctx, name, apiKey, apiSecret, passphrase, ed25519Key, wsAPIKey)
}

// connectOverseas 해외 거래소 연결 (내부 사용)
func (em *ExchangeManager) connectOverseas(ctx context.Context, name, apiKey, apiSecret, passphrase, ed25519Key, wsAPIKey string) error {
	var adapter exchange.OverseasExchange

	switch name {
	case "binance":
		adapter = binance.NewAdapter(apiKey, apiSecret)
	case "bybit":
		adapter = bybit.NewAdapter(apiKey, apiSecret)
	case "okx":
		adapter = okx.NewAdapter(apiKey, apiSecret, passphrase)
	case "gate":
		adapter = gate.NewAdapter(apiKey, apiSecret)
	case "bitget":
		adapter = bitget.NewAdapter(apiKey, apiSecret, passphrase)
	case "kucoin":
		adapter = kucoin.NewAdapter(apiKey, apiSecret, passphrase)
	case "mexc":
		adapter = mexc.NewAdapter(apiKey, apiSecret)
	case "htx":
		adapter = htx.NewAdapter(apiKey, apiSecret)
	case "hyperliquid":
		// apiKey = 메인 지갑 주소 (HYPERLIQUID_WALLET_ADDRESS). API Wallet private key(apiSecret)로
		// 서명하되 조회 대상은 메인 지갑이어야 하므로 walletAddress를 명시 전달.
		var err error
		adapter, err = hyperliquid.NewAdapter(apiKey, apiSecret, false)
		if err != nil {
			return fmt.Errorf("hyperliquid adapter init: %w", err)
		}
	case "lighter":
		var err error
		adapter, err = lighter.NewAdapter(apiSecret, 0, false)
		if err != nil {
			return fmt.Errorf("lighter adapter init: %w", err)
		}
	default:
		return fmt.Errorf("unknown overseas exchange: %s", name)
	}

	if err := adapter.Connect(ctx); err != nil {
		em.logger.Error().Err(err).Str("exchange", name).Msg("해외 거래소 연결 실패")
		return err
	}

	em.overseasExchanges[name] = adapter
	em.logger.Info().Str("exchange", name).Msg("해외 거래소 연결 완료")
	return nil
}

// ConnectExchange 특정 거래소 연결 (공개 메서드)
func (em *ExchangeManager) ConnectExchange(ctx context.Context, name, apiKey, apiSecret, passphrase, ed25519Key, wsAPIKey string) error {
	em.mu.Lock()
	defer em.mu.Unlock()

	isDomestic := name == "upbit" || name == "bithumb"

	if isDomestic {
		return em.connectDomestic(ctx, name, apiKey, apiSecret)
	}
	return em.connectOverseas(ctx, name, apiKey, apiSecret, passphrase, ed25519Key, wsAPIKey)
}

// GetDomesticExchange 국내 거래소 인스턴스 반환
func (em *ExchangeManager) GetDomesticExchange(name string) (exchange.DomesticExchange, bool) {
	em.mu.RLock()
	defer em.mu.RUnlock()

	ex, ok := em.domesticExchanges[name]
	return ex, ok
}

// GetOverseasExchange 해외 거래소 인스턴스 반환
func (em *ExchangeManager) GetOverseasExchange(name string) (exchange.OverseasExchange, bool) {
	em.mu.RLock()
	defer em.mu.RUnlock()

	ex, ok := em.overseasExchanges[name]
	return ex, ok
}

// GetAllDomesticExchanges 모든 국내 거래소 반환
func (em *ExchangeManager) GetAllDomesticExchanges() map[string]exchange.DomesticExchange {
	em.mu.RLock()
	defer em.mu.RUnlock()

	result := make(map[string]exchange.DomesticExchange, len(em.domesticExchanges))
	for k, v := range em.domesticExchanges {
		result[k] = v
	}
	return result
}

// GetAllOverseasExchanges 모든 해외 거래소 반환
func (em *ExchangeManager) GetAllOverseasExchanges() map[string]exchange.OverseasExchange {
	em.mu.RLock()
	defer em.mu.RUnlock()

	result := make(map[string]exchange.OverseasExchange, len(em.overseasExchanges))
	for k, v := range em.overseasExchanges {
		result[k] = v
	}
	return result
}

// IsDomestic 국내 거래소 여부 확인
func (em *ExchangeManager) IsDomestic(name string) bool {
	lower := strings.ToLower(name)
	return lower == "upbit" || lower == "bithumb"
}

// GetConnectedExchanges 연결된 거래소 목록
func (em *ExchangeManager) GetConnectedExchanges() []string {
	em.mu.RLock()
	defer em.mu.RUnlock()

	var exchanges []string
	for name := range em.domesticExchanges {
		exchanges = append(exchanges, name)
	}
	for name := range em.overseasExchanges {
		exchanges = append(exchanges, name)
	}
	return exchanges
}

// IsConnected 거래소 연결 상태 확인
func (em *ExchangeManager) IsConnected(name string) bool {
	em.mu.RLock()
	defer em.mu.RUnlock()

	if domestic, ok := em.domesticExchanges[name]; ok {
		return domestic.IsConnected()
	}
	if overseas, ok := em.overseasExchanges[name]; ok {
		return overseas.IsConnected()
	}
	return false
}

// RefreshBalances 모든 거래소 잔액 갱신
func (em *ExchangeManager) RefreshBalances(ctx context.Context) (map[string]*ExchangeBalance, error) {
	em.mu.Lock()
	defer em.mu.Unlock()

	results := make(map[string]*ExchangeBalance)
	var wg sync.WaitGroup
	var mu sync.Mutex

	// 국내 거래소 잔액 조회
	for name, ex := range em.domesticExchanges {
		wg.Add(1)
		go func(name string, ex exchange.DomesticExchange) {
			defer wg.Done()

			balances, err := ex.GetAllBalances(ctx)
			if err != nil {
				em.logger.Error().Err(err).Str("exchange", name).Msg("잔액 조회 실패")
				mu.Lock()
				results[name] = &ExchangeBalance{
					Exchange:  name,
					Connected: false,
				}
				mu.Unlock()
				return
			}

			// 국내 거래소: USDT/스테이블코인만 합산
			stablecoins := map[string]bool{
				"USDT": true, "USDC": true, "BUSD": true, "TUSD": true,
				"FDUSD": true, "USDE": true, "DAI": true, "USDP": true,
			}

			total := 0.0
			for coin, bal := range balances {
				if stablecoins[coin] && bal.Total > 0 {
					total += bal.Total
				}
			}

			mu.Lock()
			results[name] = &ExchangeBalance{
				Exchange:     name,
				DisplayName:  name,
				SpotTotal:    total,
				SpotBalances: balances,
				Connected:    true,
			}
			mu.Unlock()
		}(name, ex)
	}

	// 해외 거래소 잔액 조회
	for name, ex := range em.overseasExchanges {
		wg.Add(1)
		go func(name string, ex exchange.OverseasExchange) {
			defer wg.Done()

			// 현물 잔액 - 전체 잔고 조회 후 USD 가치 합산
			allBals, spotErr := ex.GetAllBalances(ctx)
			// 선물 잔액
			futuresBal, futuresErr := ex.GetFuturesBalance(ctx)

			if spotErr != nil && futuresErr != nil {
				em.logger.Error().Str("exchange", name).Msg("잔액 조회 실패")
				mu.Lock()
				results[name] = &ExchangeBalance{
					Exchange:  name,
					Connected: false,
				}
				mu.Unlock()
				return
			}

			spotTotal := 0.0
			futuresTotal := 0.0

			if spotErr == nil && allBals != nil {
				stablecoins := map[string]bool{
					"USDT": true, "USDC": true, "BUSD": true, "TUSD": true,
					"FDUSD": true, "USD1": true, "USDE": true, "DAI": true, "USDP": true,
				}
				for coin, bal := range allBals {
					if stablecoins[coin] {
						spotTotal += bal.Total
					} else if bal.Total > 0 {
						// 비스테이블 코인: 가격 조회하여 USD 환산
						priceCtx, priceCancel := context.WithTimeout(ctx, 3*time.Second)
						if ticker, err := ex.GetSpotTicker(priceCtx, coin); err == nil && ticker != nil && ticker.Price > 0 {
							spotTotal += bal.Total * ticker.Price
						}
						priceCancel()
					}
				}
			}
			futuresFree := 0.0
			if futuresErr == nil && futuresBal != nil {
				futuresTotal = futuresBal.Total
				futuresFree = futuresBal.Free
			}

			mu.Lock()
			results[name] = &ExchangeBalance{
				Exchange:     name,
				DisplayName:  name,
				SpotTotal:    spotTotal,
				FuturesTotal: futuresTotal,
				FuturesFree:  futuresFree,
				Connected:    true,
			}
			mu.Unlock()
		}(name, ex)
	}

	wg.Wait()

	em.balances = results
	return results, nil
}

// StartBalanceRefreshLoop 백그라운드 잔고 갱신 루프 시작
// 시작 시 즉시 1회 갱신 후, interval 주기로 반복 갱신
func (em *ExchangeManager) StartBalanceRefreshLoop(ctx context.Context, interval time.Duration) {
	// 즉시 1회 갱신
	if _, err := em.RefreshBalances(ctx); err != nil {
		em.logger.Warn().Err(err).Msg("초기 잔고 갱신 실패")
	} else {
		em.logger.Info().Msg("초기 잔고 갱신 완료")
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if _, err := em.RefreshBalances(ctx); err != nil {
					em.logger.Warn().Err(err).Msg("백그라운드 잔고 갱신 실패")
				}
			case <-ctx.Done():
				em.logger.Info().Msg("잔고 갱신 루프 종료")
				return
			}
		}
	}()

	em.logger.Info().Dur("interval", interval).Msg("잔고 갱신 루프 시작")
}

// GetCachedBalance 캐시된 특정 거래소 잔고 반환
func (em *ExchangeManager) GetCachedBalance(exchangeName string) (*ExchangeBalance, bool) {
	em.mu.RLock()
	defer em.mu.RUnlock()
	bal, ok := em.balances[exchangeName]
	return bal, ok
}

// GetBalances 캐시된 잔액 반환
func (em *ExchangeManager) GetBalances() map[string]*ExchangeBalance {
	em.mu.RLock()
	defer em.mu.RUnlock()

	result := make(map[string]*ExchangeBalance, len(em.balances))
	for k, v := range em.balances {
		result[k] = v
	}
	return result
}

// GetTotalBalance 전체 잔액 합계
func (em *ExchangeManager) GetTotalBalance() map[string]float64 {
	em.mu.RLock()
	defer em.mu.RUnlock()

	spotTotal := 0.0
	futuresTotal := 0.0

	for _, bal := range em.balances {
		spotTotal += bal.SpotTotal
		futuresTotal += bal.FuturesTotal
	}

	return map[string]float64{
		"spot":    spotTotal,
		"futures": futuresTotal,
		"total":   spotTotal + futuresTotal,
	}
}
