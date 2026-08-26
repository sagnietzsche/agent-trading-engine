# War-like situation — severity 8/10 · geopolitical

## The real-world pattern

Hostilities erupt and the market reprices the world in minutes. **Energy
surges** — supply is at risk and every barrel is strategic. Anything
dependent on supply chains (shipping, logistics, semiconductors) is crushed,
because the same airspace and sea lanes that move troops used to move cargo.
Defense-adjacent names gap up on order-book revisions. The spike is violent
on both sides: volatility explodes, market makers widen spreads to survive
and pull their depth, so a thin, gap-driven tape whipsaws anyone who chases.
Every headline is a 5% move, and most of them reverse.

## Event definition

```json
{
  "id": "war_like_situation",
  "name": "War-like situation",
  "severity": 8,
  "kind": "geopolitical",
  "duration_ticks": 400,
  "news": [
    "Hostilities erupt; airspace over key trade routes closes",
    "Energy infrastructure struck; supply at risk",
    "Commodity shipping lanes rerouted; freight costs soar",
    "Defense budgets revised sharply upward"
  ],
  "shock": {
    "symbols": {
      "ZEPH": 0.45,
      "DRCT": 0.15,
      "HELX": 0.10,
      "NOVA": -0.15,
      "QNTM": -0.25,
      "ORBT": -0.35
    },
    "ticks": 15,
    "decay": 0.01
  },
  "drift": {
    "ZEPH": 0.0015,
    "ORBT": -0.001,
    "QNTM": -0.0005
  },
  "volatility": 4.0,
  "spread_multiplier": 3.0,
  "liquidity": { "levels": 1, "size_multiplier": 0.4 },
  "circuit_breaker": { "drop_pct": 0.25, "halt_ticks": 15 },
  "solidarity": { "gini_target_multiplier": 1.3, "gift_rate_multiplier": 1.25 },
  "rationale": "Energy and defensive names gap up while anything supply-chain dependent is crushed; volatility and spreads explode as market makers pull liquidity, so the tape whipsaws whoever chases the news."
}
```

## What it does to the market

| Engine knob | Effect |
|---|---|
| Fair values | `ZEPH` +45% (energy), `DRCT` +15%, `HELX` +10%; `ORBT` −35% (logistics), `QNTM` −25%, `NOVA` −15%; shock over 15 ticks, slow 1%/tick decay |
| Drift | `ZEPH` grinds +0.15%/tick while `ORBT` bleeds −0.1%/tick — the rotation keeps going all event |
| Volatility | ×4.0 random-walk shocks — every headline is a 5% move |
| Spread | ×3.0 wider market-maker spreads |
| Liquidity | one quote level per side at 40% size — a shallow, gap-prone book |
| Circuit breaker | halts for 15 ticks once any symbol drops 25% from the event start |
| Solidarity flow | tolerance 1.3×, giving 1.25× — the collective keeps redistributing into the chaos |

## How agents should react

- **Ride the rotation, don't fight it**: long `ZEPH` exposure and no short
  against `ORBT`'s bleed. The drift is your friend while the event lasts.
- **Beware the gap**: with one level of depth, a market order sweeps the
  whole book. Limit orders only — and expect to wait through halts.
- **News is a trap**: at ×4.0 volatility the "relief" spikes reverse. Take
  profits into the bid, don't add on the pop.
- **Cooperation still pays**: giving mandates route to the worst-off first;
  in a war the worst-off are the ones short `ORBT`. The solidarity bot keeps
  buying their pain.

## Rationale

War is mayhem because it is *directional violence*: one sector's windfall is
another's collapse, in the same tick, through a book that has almost no depth
to absorb it. Chasing the news in either direction gets you filled at the
worst price in the spread and then whipsawed. The agents that win are the
ones with a sector view, small size, and patience for the halts.
