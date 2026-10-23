export type ActionIconName = 'create' | 'view' | 'delete' | 'edit' | 'archive' | 'close' | 'up' | 'down' | 'back' | 'sort' | 'left' | 'right' | 'check' | 'download' | 'forward' | 'turn' | 'share' | 'layout' | 'menu'

export function ActionIcon({name}: {name: ActionIconName}) {
  const paths = {
    menu: <path d="M4 6h16M4 12h16M4 18h16" />,
    layout: <><rect x="3" y="4" width="18" height="16" rx="2" /><path d="M3 9h18M9 9v11M15 9v11" /></>,
    share: <><circle cx="18" cy="5" r="3" /><circle cx="6" cy="12" r="3" /><circle cx="18" cy="19" r="3" /><path d="m8.6 10.5 6.8-4M8.6 13.5l6.8 4" /></>,
    create: <path d="M12 5v14M5 12h14" />,
    view: <><path d="M2.5 12s3.5-5.5 9.5-5.5 9.5 5.5 9.5 5.5-3.5 5.5-9.5 5.5S2.5 12 2.5 12z" /><circle cx="12" cy="12" r="2.5" /></>,
    delete: <path d="M4 7h16M9 7V4h6v3m-9 0 1 13h10l1-13M10 11v5m4-5v5" />,
    edit: <><path d="m16 3 5 5-12 12-6 1 1-6Z" /><path d="m14 5 5 5" /></>,
    archive: <><path d="M4 8h16v12H4zM3 4h18v4H3zM10 12h4" /></>,
    close: <path d="m6 6 12 12M6 18 18 6" />,
    up: <path d="m6 15 6-6 6 6" />,
    down: <path d="m6 9 6 6 6-6" />,
    sort: <path d="m8 9 4-4 4 4m-8 6 4 4 4-4" />,
    left: <path d="m15 6-6 6 6 6" />,
    right: <path d="m9 6 6 6-6 6" />,
    check: <path d="m5 12 4 4L19 6" />,
    download: <path d="M12 3v12m-5-5 5 5 5-5M5 16v5h14v-5" />,
    forward: <path d="m12 5 7 7-7 7M5 12h14" />,
    turn: <path d="M5 4v10h14m-5-5 5 5-5 5" />,
    back: <path d="m12 5-7 7 7 7M5 12h14" />,
  }
  return <svg className="action-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{paths[name]}</svg>
}
