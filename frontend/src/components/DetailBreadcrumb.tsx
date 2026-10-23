import {ReturnButton} from './ReturnButton'
interface Props {
  parent: string
  current: string
  back: () => void
}

export function DetailBreadcrumb({parent, current, back}: Props) {
  return <nav className="detail-return" aria-label={`Navigation from ${current}`}><ReturnButton destination={parent === 'Back' ? undefined : parent} onClick={back} /></nav>
}
