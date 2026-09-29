# Position Monitor (English)

> [🇰🇷 한국어](README.md) | English

> Multi-exchange hedge position monitor with portfolio dashboard.
> **Read-only.** Does not place orders.

A desktop app (Wails + React) that connects to 12 crypto exchanges with **read-only** API keys and shows:

1. **Hedge pairs** — automatic spot↔futures matching across exchanges (e.g. Upbit BTC spot vs Binance BTC short)
2. **Unmatched positions** — single-leg positions that broke a hedge, with PnL
3. **Balance dashboard** — total assets (USD/KRW), 24h change, cumulative return, BTC chart, asset distribution
4. **Settings** — exchange enable/disable view
5. **Telegram liquidation alerts (optional, off by default)** — see [Telegram liquidation alerts](#telegram-liquidation-alerts-optional)

The hedge pairs table shows the liquidation price for Binance, Bybit, OKX, Gate, Bitget and Hyperliquid futures. Its mark/liquidation prices use 2 decimals at ≥ 0.1 and 5 decimals below 0.1 (ultra-low prices: 3 significant digits); quantities in the position tables use 2 decimals (tiny quantities: 3 significant digits).

No trading code is included. The exchange adapters expose only ticker / orderbook / balance / position read methods.

## Supported exchanges (12)

Korean: Upbit, Bithumb
Overseas (spot + futures): Binance, Bybit, OKX, Gate, Bitget, KuCoin, MEXC, HTX
DEX (futures only): Hyperliquid, Lighter

## Stack

- Backend: Go 1.25 + [Wails v2.12](https://wails.io)
- Frontend: React 18 + TypeScript 5 + Vite 5 + Tailwind CSS v4 + Zustand
- DB: SQLite (modernc.org/sqlite, WAL mode) — local snapshots only
- Build: `wails build` → macOS `.app`

## Quick start

### Prerequisites

- Go 1.25+
- Node 18+
- [Wails CLI](https://wails.io/docs/gettingstarted/installation): `go install github.com/wailsapp/wails/v2/cmd/wails@latest`

### Setup

```bash
git clone https://github.com/londonpotato1/position-monitor.git
cd position-monitor

# 1. Copy config templates
cp config.yaml.example config.yaml
cp .env.example .env

# 2. Edit .env — fill in your read-only API keys (see "API keys" below)
# 3. Edit config.yaml — set `enabled: true` for exchanges you have keys for

# 4. Build the app
wails build

# 5. Run
open build/bin/position-monitor.app
```

For development with hot reload:

```bash
wails dev
```

## API keys

**Use read-only keys only.** This app does not need trade, withdrawal, or transfer permissions. Restrict your API keys to:

- Read account / balances
- Read positions
- Read tickers / orderbooks

`.env` is a plain key=value file at the project root. **Variable names must use the `_API_KEY` / `_API_SECRET` form** (e.g. `BINANCE_API_KEY=...`).

```bash
BINANCE_API_KEY=your_read_only_key
BINANCE_API_SECRET=your_read_only_secret

BYBIT_API_KEY=...
BYBIT_API_SECRET=...
# ... same pattern for every exchange
```

DEX exchanges use special formats:
- `HYPERLIQUID_WALLET_ADDRESS` + `HYPERLIQUID_PRIVATE_KEY`
- `LIGHTER_ACCOUNT_INDEX` + `LIGHTER_PRIVATE_KEY` + `LIGHTER_API_KEY_INDEX`

**Backward-compat aliases**: shorter forms like `BINANCE_SECRET` auto-alias to `_API_SECRET` (for users coming from internal bots). The `_KEY` side has no aliases — **always use `_API_KEY` explicitly**.

See [`.env.example`](.env.example) for the full list.

## How it works

- The app polls each enabled exchange every `position.refresh_interval` seconds (default 5s)
- Spot balances and futures positions are matched by coin symbol — same coin, opposite direction → hedge pair
- Unmatched legs (only one side present) appear in the Unmatched tab
- The Balance Dashboard aggregates USD value across exchanges using a live USDT/KRW rate (Upbit → Bithumb fallback)
- Daily snapshots are stored locally in `data/` (SQLite)

## Telegram liquidation alerts (optional)

Each futures-short row in the hedge pairs table has an alert ON/OFF toggle. Only rows you turn ON send alerts, to your own Telegram bot. Notifications only — no trading. Off by default. Alert text is in Korean.

- Supported exchanges: Binance, Bybit, OKX, Gate, Bitget, Hyperliquid
- Distance to liquidation: 100 / 80 / 70 / 60 / 50% once on entry; while in the tier, 40% repeats every 4h, 30% every 1h, 15% every 15m, 5% every 2m. A separate alert when the estimated liquidation price is reached/exceeded
- Price rise vs futures entry: +20 / 30 / 40%
- Urgent alerts (≤15% tiers, reached/exceeded, rise alerts) are sent 10 times, 1 second apart
- Status messages: each time monitoring starts, one "monitoring started (N ON)" message listing the ON rows (once at least one row is ON). If a position cannot be checked (data older than 30 s, or exchange disconnected / fetch failed) for over 60 s, one "watch unavailable" message, then one "watch recovered" when it is back or one "watch ended" if the position is gone. Tier state is in memory only, so after an app restart the current tier is alerted once again
- Toggles are stored in `data/liq_alerts.db`

To enable:

1. Set `telegram.enabled: true` in `config.yaml`
2. Set `TELEGRAM_BOT_TOKEN` and `TELEGRAM_CHAT_ID` in `.env`
3. Restart the app (both are read only at startup), then turn ON the alert for the rows you want

If a row is ON but alerts cannot be delivered (switch off / missing keys), the hedge pairs table shows a banner with the reason.

## What this app does NOT do

- Place orders
- Cancel orders
- Close positions
- Set leverage
- Withdraw or transfer funds
- Sync to Google Sheets / Excel

The interface and adapter code for these features has been removed entirely. If you fork and want trading, you'll have to implement it yourself. The only outbound message the app sends is the optional Telegram liquidation alert to your own bot.

## License

MIT — see [`LICENSE`](LICENSE).

## Disclaimer

This software is provided for **informational purposes only**. You are responsible for the security of your API keys, the accuracy of any data displayed, and any decisions you make based on it. The authors are not liable for trading losses, lost data, or anything else.
