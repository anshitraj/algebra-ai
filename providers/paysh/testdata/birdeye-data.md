# Birdeye Data
> Pay-per-request DeFi market data, token analytics, and wallet intelligence across Solana and 15+ chains. Covers token prices, OHLCV charts, holder distribution, smart money flows, meme signals, trade history, and security scores without API keys.

## Agent Summary
- FQN: `birdeye/data`
- Category: finance
- Operator: birdeye
- Origin: birdeye
- Version: 
- Endpoints: 46
- Pricing: free
- HTML page: https://pay.sh/api/birdeye/data
- Markdown page: https://pay.sh/api/birdeye/data/index.md

## Service URLs
- Gateway: https://public-api.birdeye.so
- Source: solana-foundation/pay-skills
- Source path: providers/birdeye/data/PAY.md
- Source skill: pay-skills

## Use Case
Use for fetching token prices, OHLCV charts, holder distribution, top trader rankings, smart money flows, token security assessments, meme token signals, new token listings, and trade history on Solana and multi-chain DeFi.

## Endpoint Table
| Method | Path | Pricing | Description |
| --- | --- | --- | --- |
| GET | x402/defi/historical_price_unix | free | Get token price at a specific Unix timestamp |
| GET | x402/defi/history_price | free | Get historical price series for a token or pair |
| GET | x402/defi/ohlcv | free | Get OHLCV candlestick data for a token |
| GET | x402/defi/ohlcv/base_quote | free | Get OHLCV candlestick data by base and quote token addresses |
| GET | x402/defi/ohlcv/pair | free | Get OHLCV candlestick data for a trading pair |
| GET | x402/defi/price | free | Get real-time price for a token |
| GET | x402/defi/price_volume/single | free | Get current price and 24h volume for a single token |
| GET | x402/defi/token_creation_info | free | Get creation info and genesis data for a token |
| GET | x402/defi/token_overview | free | Get token overview: price, volume, liquidity, and metadata |
| GET | x402/defi/token_security | free | Get security analysis and rug pull risk indicators for a token |
| GET | x402/defi/token_trending | free | List currently trending tokens by trading activity |
