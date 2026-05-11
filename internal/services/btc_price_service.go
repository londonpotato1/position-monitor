// Package services - BTCPriceService: Binance fapi BTC 가격/kline 조회
package services

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/rs/zerolog"
)

const (
	binanceFapiKlinesURL = "https://fapi.binance.com/fapi/v1/klines"
	binanceFapiPriceURL  = "https://fapi.binance.com/fapi/v1/ticker/price"
)

// interval별 기본 limit
var btcKlineDefaultLimits = map[string]int{
	"15m": 96,
	"1h":  168,
	"4h":  360,
	"1d":  1500,
	"1w":  200,
}

// KlinePoint BTC kline 데이터 포인트
type KlinePoint struct {
	Timestamp int64   `json:"timestamp"` // Unix ms
	Open      float64 `json:"open"`
	High      float64 `json:"high"`
	Low       float64 `json:"low"`
	Close     float64 `json:"close"`
	Volume    float64 `json:"volume"`
}

// BTCPriceService Binance fapi BTC 가격 서비스
type BTCPriceService struct {
	client *http.Client
	logger zerolog.Logger
}

// NewBTCPriceService 새 BTCPriceService 생성
func NewBTCPriceService(logger zerolog.Logger) *BTCPriceService {
	return &BTCPriceService{
		client: &http.Client{Timeout: 10 * time.Second},
		logger: logger.With().Str("component", "btc_price_service").Logger(),
	}
}

// GetKlines BTC kline 데이터 조회
// interval: "15m", "1h", "4h", "1d", "1w"
// limit: 0이면 interval별 기본값 사용
func (s *BTCPriceService) GetKlines(interval string, limit int) ([]KlinePoint, error) {
	validIntervals := map[string]bool{
		"15m": true,
		"1h":  true,
		"4h":  true,
		"1d":  true,
		"1w":  true,
	}
	if !validIntervals[interval] {
		return nil, fmt.Errorf("유효하지 않은 interval: %s", interval)
	}

	if limit <= 0 {
		limit = btcKlineDefaultLimits[interval]
	}
	if limit > 1500 {
		limit = 1500
	}

	url := fmt.Sprintf("%s?symbol=BTCUSDT&interval=%s&limit=%d",
		binanceFapiKlinesURL, interval, limit)

	resp, err := s.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("Binance klines 요청 실패: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Binance klines API 에러 %d: %s", resp.StatusCode, string(body))
	}

	// Binance klines 응답: [[timestamp, open, high, low, close, volume, ...], ...]
	var raw [][]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("klines 응답 파싱 실패: %w", err)
	}

	points := make([]KlinePoint, 0, len(raw))
	for _, row := range raw {
		if len(row) < 6 {
			continue
		}

		// index 0: timestamp (number)
		var ts int64
		if err := json.Unmarshal(row[0], &ts); err != nil {
			return nil, fmt.Errorf("timestamp 파싱 실패: %w", err)
		}

		// index 1..5: open/high/low/close/volume (quoted strings in Binance API)
		fields := make([]float64, 5)
		for i, idx := range []int{1, 2, 3, 4, 5} {
			var raw string
			if err := json.Unmarshal(row[idx], &raw); err != nil {
				return nil, fmt.Errorf("kline field[%d] 파싱 실패: %w", idx, err)
			}
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return nil, fmt.Errorf("kline field[%d] float 변환 실패: %w", idx, err)
			}
			fields[i] = v
		}

		points = append(points, KlinePoint{
			Timestamp: ts,
			Open:      fields[0],
			High:      fields[1],
			Low:       fields[2],
			Close:     fields[3],
			Volume:    fields[4],
		})
	}

	s.logger.Debug().
		Str("interval", interval).
		Int("count", len(points)).
		Msg("BTC klines 조회 완료")

	return points, nil
}

// GetCurrentPrice BTC 현재가 조회
func (s *BTCPriceService) GetCurrentPrice() (float64, error) {
	url := binanceFapiPriceURL + "?symbol=BTCUSDT"

	resp, err := s.client.Get(url)
	if err != nil {
		return 0, fmt.Errorf("Binance ticker 요청 실패: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("Binance ticker API 에러 %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Price string `json:"price"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("ticker 응답 파싱 실패: %w", err)
	}

	price, err := strconv.ParseFloat(result.Price, 64)
	if err != nil {
		return 0, fmt.Errorf("가격 float 변환 실패: %w", err)
	}

	s.logger.Debug().Float64("price", price).Msg("BTC 현재가 조회 완료")
	return price, nil
}
