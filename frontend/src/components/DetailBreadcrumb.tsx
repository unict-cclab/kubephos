interface Props {
  parent: string
  current: string
  back: () => void
}

export function DetailBreadcrumb({parent, current, back}: Props) {
  return <nav className="detail-breadcrumbs" aria-label="Detail breadcrumb"><button onClick={back}>{parent}</button><span aria-hidden="true">›</span><strong aria-current="page">{current}</strong></nav>
}
