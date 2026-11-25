import {useEffect, useRef, useState, type MouseEvent} from 'react'

// Sticky in-page navigation of a results page: one link per section, the section under
// the bar highlighted while scrolling; a link opens its (collapsed) section and jumps to
// it. A floating "Back to top" button appears once the page is scrolled past the overview.

export interface SectionLink {
  id: string
  label: string
  // Element id the link jumps to.
  target: string
}

const reducedMotion = () => typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches

export function scrollToElement(element: HTMLElement) {
  element.scrollIntoView?.({behavior: reducedMotion() ? 'auto' : 'smooth', block: 'start'})
}

export function SectionNav({links, onJump, top}: {links: SectionLink[]; onJump: (link: SectionLink) => void; top: string}) {
  const [current, setCurrent] = useState<string | undefined>(links[0]?.id)
  const [scrolled, setScrolled] = useState(false)
  // Links whose target is rendered (ids joined), measured after each layout change.
  const [present, setPresent] = useState<string | null>(null)
  const nav = useRef<HTMLElement>(null)
  const pinned = useRef<{id: string; target: string; at: number} | undefined>(undefined)
  const key = links.map(link => link.target).join('|')
  useEffect(() => {
    let frame = 0
    const measure = () => {
      frame = 0
      const offset = (nav.current?.getBoundingClientRect().bottom ?? 0) + 24
      let active = links[0]?.id
      for (const link of links) {
        const element = document.getElementById(link.target)
        if (element && element.getBoundingClientRect().top <= offset) active = link.id
      }
      // A section just jumped to stays highlighted while its heading is in the upper half
      // of the viewport (at the bottom of the page short sections never reach the bar).
      const pinnedElement = pinned.current ? document.getElementById(pinned.current.target) : null
      const pinnedTop = pinnedElement?.getBoundingClientRect().top
      if (pinned.current && (Date.now() - pinned.current.at < 1500 || (pinnedTop !== undefined && pinnedTop >= -4 && pinnedTop < window.innerHeight / 2))) active = pinned.current.id
      else pinned.current = undefined
      setCurrent(active)
      setPresent(links.filter(link => document.getElementById(link.target)).map(link => link.id).join('|'))
      const page = document.getElementById(top)
      setScrolled(Boolean(page && page.getBoundingClientRect().top < -480))
    }
    const schedule = () => {if (!frame) frame = requestAnimationFrame(measure)}
    measure()
    document.addEventListener('scroll', schedule, {capture: true, passive: true})
    window.addEventListener('resize', schedule)
    return () => {document.removeEventListener('scroll', schedule, {capture: true}); window.removeEventListener('resize', schedule); if (frame) cancelAnimationFrame(frame)}
  }, [key, top])
  // Keep the highlighted link visible in the horizontally scrolling bar (phones).
  useEffect(() => {
    const link = nav.current?.querySelector<HTMLElement>('[aria-current="location"]')
    const bar = nav.current?.querySelector<HTMLElement>('.section-nav-list')
    if (link && bar && bar.scrollWidth > bar.clientWidth) bar.scrollTo?.({left: link.offsetLeft - 16, behavior: 'auto'})
  }, [current])
  const visible = present === null ? links : links.filter(link => present.split('|').includes(link.id))
  if (visible.length < 2) return null
  const jump = (event: MouseEvent<HTMLAnchorElement>, link: SectionLink) => {
    event.preventDefault()
    pinned.current = {id: link.id, target: link.target, at: Date.now()}
    setCurrent(link.id)
    onJump(link)
  }
  const backToTop = () => {
    const page = document.getElementById(top)
    if (!page) return
    scrollToElement(page)
    page.querySelector<HTMLElement>('h2')?.focus?.({preventScroll: true})
  }
  return <>
    <nav className="section-nav" aria-label="Sections of the results" ref={nav}>
      <ul className="section-nav-list">{visible.map(link => <li key={link.id}><a href={`#${link.target}`} aria-current={current === link.id ? 'location' : undefined} onClick={event => jump(event, link)}>{link.label}</a></li>)}</ul>
    </nav>
    {scrolled && <button type="button" className="back-to-top" onClick={backToTop}>
      <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true"><path d="M8 13V3M3.5 7.5 8 3l4.5 4.5" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" /></svg>
      Back to top
    </button>}
  </>
}
