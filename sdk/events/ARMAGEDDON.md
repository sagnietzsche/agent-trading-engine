# Armageddon — severity 10/10 · systemic

## The real-world pattern

Total loss of confidence. Safe havens fail too — when *everything* is being
sold, "safe" just means "sells off last". Every asset gaps down, circuit
breakers trip in sequence, and when trading resumes the gap is bigger.
Liquidity evaporates to a single thin quote from a market maker that is
pricing for its own survival. There is no bid, only the next lower price.
In this universe one buyer remains: the **solidarity bot**, whose mandate is
to keep giving into the bids of the worst-off. Armageddon is the test of
whether the collective machinery can hold a floor under a market that has
decided nothing is worth anything.

## Event definition

```json
{
  "id": "armageddon",
  "name": "Armageddon",
  "severity": 10,
  "kind": "systemic",
  "duration_ticks": 300,
  "news": [
    "Clearing failures reported across major venues",
    "Circuit breakers trip in every session",
    "Safe-haven assets also sold; cash is king",
    "Emergency liquidity facilities announced — too late?"
  ],
  "shock": {
    "symbols": {
      "QNTM": -0.80,
      "ORBT": -0.75,
      "NOVA": -0.70,
      "HELX": -0.60,
      "DRCT": -0.55,
      "ZEPH": -0.45
    },
    "ticks": 10,
    "decay": 0.0
  },
  "drift": {
    "NOVA": -0.002,
    "QNTM": -0.002,
    "ORBT": -0.002,
    "HELX": -0.0015,
    "DRCT": -0.0015,
    "ZEPH": -0.001
  },
  "volatility": 6.0,
  "spread_multiplier": 5.0,
  "liquidity": { "levels": 1, "size_multiplier": 0.2 },
  "circuit_breaker": { "drop_pct": 0.15, "halt_ticks": 30 },
  "solidarity": { "gini_target_multiplier": 2.0, "gift_rate_multiplier": 3.0 },
  "rationale": "Total loss of confidence: every asset gaps down, liquidity evaporates to a single thin quote, and halts trip constantly. The solidarity machinery is the only bid left in town."
}
```

## What it does to the market

| Engine knob | Effect |
|---|---|
| Fair values | everything −45% to −80% over just 10 ticks, with **no decay** — the collapse is permanent for the whole event |
| Drift | −0.1%/tick to −0.2%/tick on every symbol — even after the crash, the market keeps sinking |
| Volatility | ×6.0 random-walk shocks — gaps inside gaps |
| Spread | ×5.0 wider market-maker spreads |
| Liquidity | one quote level per side at 20% size — a single thin quote is the entire book |
| Circuit breaker | halts for 30 ticks once any symbol drops 15% from the event start — halts trip constantly, trading is mostly suspended |
| Solidarity flow | tolerance doubles (2.0×) and giving flow triples (3.0×) — the solidarity bot floods what little bid remains |

## How agents should react

- **Cash is the only position**. The drift is negative everywhere; there is
  no rotation to hide in. If you're not already out, you sell into nothing.
- **Size is survival**. The book has one level at 20% size — a market order
  of any real size moves the price by itself. Everything is limit orders,
  and most of them rest forever.
- **The halts are the market**. 30-tick halts triggered by a 15% drop mean
  the tape is mostly dark. Strategy that assumes continuous prices will
  misprice every fill that does happen.
- **Watch the solidarity bot**. It is the only buyer. Beneficiaries that
  leave bids resting under its giving path get filled first; contributors
  that give into a collapsing market are the ones holding the floor. The
  scoreboard at the end is about who helped hold it together, not who
  preserved capital.

## Rationale

Armageddon is mayhem squared: the crash is instantaneous, permanent, and
unrelenting, liquidity is a single thin quote, and the market is halted more
than it trades. Nothing about normal trading applies. The only interesting
question the scenario asks is whether the collective-welfare machinery —
mandates, need-priority matching, the solidarity bot's giving — can behave
like a lender of last resort when every other participant has run for the
exit.
