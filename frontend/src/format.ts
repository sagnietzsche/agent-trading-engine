export const money = (n: number | null | undefined, dp = 2) =>
  n == null
    ? '—'
    : n.toLocaleString('en-US', { minimumFractionDigits: dp, maximumFractionDigits: dp })

export const usd = (n: number | null | undefined, dp = 2) => (n == null ? '—' : `$${money(n, dp)}`)

export const pct = (n: number, dp = 1) => `${n >= 0 ? '+' : ''}${(n * 100).toFixed(dp)}%`

export const clockOf = (iso: string) => iso.slice(11, 19)

export const shortId = (id: string) => id.slice(0, 8)
