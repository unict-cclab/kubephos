export type SectionIconKind = 'overview' | 'infrastructure' | 'kubernetes' | 'catalog' | 'application' | 'scheduler' | 'descheduler' | 'autoscaler' | 'configurations' | 'experiments' | 'suites' | 'activity'

export function SectionIcon({kind}: {kind: SectionIconKind}) {
  const shapes: Record<SectionIconKind, React.ReactNode> = {
    overview: <><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></>,
    infrastructure: <><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 8h10M7 12h10M7 16h4"/></>,
    kubernetes: <><circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="3"/><path d="M12 3v6m0 6v6M3 12h6m6 0h6"/></>,
    catalog: <><path d="M4 5h16v14H4zM4 12h16M9 5v7m6 0v7"/></>,
    application: <><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18M7 6.5h.01M10 6.5h.01M7 13h5m-5 3h9"/></>,
    scheduler: <><rect x="3" y="5" width="18" height="16" rx="2"/><path d="M7 3v4m10-4v4M3 10h18m-14 4h4m-4 3h7"/></>,
    descheduler: <><rect x="4" y="4" width="16" height="16" rx="2"/><path d="M8 8h8m-8 4h8m-8 4h4M15 15l3 3m0-3-3 3"/></>,
    autoscaler: <><path d="M4 19h16M5 16l5-5 4 3 5-7M15 7h4v4"/></>,
    configurations: <><path d="M4 6h16M4 12h16M4 18h16"/><circle cx="8" cy="6" r="2" fill="white"/><circle cx="16" cy="12" r="2" fill="white"/><circle cx="10" cy="18" r="2" fill="white"/></>,
    experiments: <><path d="M9 3h6m-4 0v7l-6 9a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2l-6-9V3M8 16h8"/></>,
    suites: <><rect x="3" y="5" width="7" height="14" rx="1"/><rect x="14" y="5" width="7" height="14" rx="1"/><path d="M5 10h3m8 4h3"/></>,
    activity: <><circle cx="12" cy="12" r="9"/><path d="M12 7v5l4 2"/></>
  }
  return <span className="section-icon" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">{shapes[kind]}</svg></span>
}
