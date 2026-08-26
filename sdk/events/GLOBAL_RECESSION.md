# Global recession — severity 6/10 · macroeconomic

## The real-world pattern

A credit freeze hits, demand collapses, and the sell-off is broad but
**not equal**. Cyclicals and growth names — semiconductors, industrials,
transport, energy — get hammered because their earnings are the most
leverage to the business cycle. Defensive sectors (healthcare, staples)
hold up: people still buy medicine and food in a downturn, they just stop
buying yachts and air freight. Meanwhile the market maker widens its
spreads and steps back, so the *liquidity* disappears at exactly the moment
everyone wants to be a seller. Panic is followed by grinding drift lower,
punctuated by relief rallies that immediately get sold.

## Event definition

```json
{
  "id": "global_recession",
  "name": "Global recession",
  "severity": 6,
  "kind": "macroeconomic",
  "duration_ticks": 600,
  "news": [
    "Central bank surprises with a 75bp emergency hike",
    "PMI data collapses into deep contraction territory",
    "Major lender suspends dividends as credit losses mount",
    "Global trade volumes fall for a sixth straight month"
  ],
  "shock": {
    "symbols": {
      "QNTM": -0.40,
      "NOVA": -0.35,
      "ORBT": -0.30,
      "ZEPH": -0.25,
      "DRCT": -0.10,
      "HELX": -0.08
    },
    "ticks": 25,
    "decay": 0.02
  },
  "drift": {
    "NOVA": -0.0003,
    "QNTM": -0.0004,
    "ORBT": -0.0003,
    "ZEPH": -0.0002,
    "HELX": 0.0002,
    "DRCT": 0.0
  },
  "volatility": 2.0,
  "spread_multiplier": 1.8,
  "liquidity": { "levels": 2, "size_multiplier": 0.6 },
  "circuit_breaker": { "drop_pct": 0.20, "halt_ticks": 10 },
  "solidarity": { "gini_target_multiplier": 1.5, "gift_rate_multiplier": 1.5 },
  "rationale": "Credit freeze + demand collapse hit cyclicals and growth hardest while healthcare and staples hold up; spreads widen and the market maker steps back just as everyone wants out."
}
```

## What it does to the market

| Engine knob | Effect |
|---|---|
| Fair values | `QNTM` −40%, `NOVA` −35%, `ORBT` −30%, `ZEPH` −25% over 25 ticks; defensives `HELX` −8%, `DRCT` −10%; the shock decays 2%/tick after the peak |
| Drift | persistent −0.03%/tick on growth/cyclicals, slight +0.02%/tick on `HELX` — the grind lower |
| Volatility | ×2.0 random-walk shocks — choppy bear market, dead-cat bounces |
| Spread | ×1.8 wider market-maker spreads |
| Liquidity | market maker keeps only 2 levels per side at 60% size — the book thins out |
| Circuit breaker | halts for 10 ticks once any symbol drops 20% from the event start |
| Solidarity flow | tolerance rises to 1.5× the normal inequality target (everyone is poorer together); giving flow strengthened 1.5× — redistribution becomes the floor under the market |

## How agents should react

- **Defensive rotation**: flee `QNTM`/`NOVA`/`ORBT` into `HELX` and `DRCT`
  before the shock fully unfolds.
- **Don't catch the knife**: with ×1.8 spreads and a thin book, market orders
  pay huge slippage — use resting limits at the bid/ask instead.
- **Respect the drift**: the lows keep coming; a bounce on news is a chance
  to lighten, not to bottom-fish.
- **Cooperation pays**: with the solidarity flow strengthened, wealthy agents
  who sell into the bids of the worst-off are rewarded twice — the mandate
  says give, and the tape is full of people who need it.

## Rationale

A recession is mayhem because it is *unrelenting*: the initial crash is only
half the pain, the drift keeps pushing, and the liquidity that usually
cushions a fall is switched off. Agents that treat it as a one-day crash get
whipsawed; agents that size down, rotate defensively, and let the solidarity
machinery redistribute survive it.
