export function LoadingScreen({ mode }: { mode: string }) {
  const label =
    mode === 'polling' ? 'reaching the exchange (polling)…' : 'connecting to the exchange…'
  return (
    <div className="loading-screen" role="status" aria-live="polite">
      <div className="loader-orb">
        <span className="loader-ring" />
        <span className="loader-ring delay-1" />
        <span className="loader-ring delay-2" />
        <span className="loader-core" />
      </div>
      <h1 className="loader-title">trading-engine</h1>
      <p className="loader-sub">{label}</p>
      <div className="loader-bar">
        <div className="loader-bar-fill" />
      </div>
      <p className="loader-foot">matched in memory · streamed to you live</p>
    </div>
  )
}
