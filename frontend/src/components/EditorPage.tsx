import {ReturnButton} from './ReturnButton'
import {useEffect, useId, useRef, useState, type ReactNode} from 'react'
import {createPortal} from 'react-dom'

export function EditorPage({title, onClose, children, className = ''}: {title: string; onClose?: () => void; children: ReactNode; className?: string}) {
  const titleID = useId()
  const heading = useRef<HTMLHeadingElement>(null)
  const content = useRef<HTMLDivElement>(null)
  const returnFocus = useRef(document.activeElement as HTMLElement | null)
  const nextSectionID = useRef(0)
  const [sections, setSections] = useState<Array<{id: string; title: string}>>([])
  const [activeSection, setActiveSection] = useState('')
  const container = document.querySelector<HTMLElement>('.page-content')
  useEffect(() => {
    const previous = returnFocus.current
    container?.classList.add('has-editor-page')
    heading.current?.closest('.editor-page')?.scrollIntoView?.({block: 'start', behavior: 'instant'})
    heading.current?.focus({preventScroll: true})
    return () => {
      container?.classList.remove('has-editor-page')
      if (previous?.isConnected) previous.focus({preventScroll: true})
    }
  }, [container])
  useEffect(() => {
    const nodes = content.current?.querySelector('.wizard-form') ? [] : content.current?.querySelectorAll<HTMLElement>('.console-section-title h3, .form-section > strong') ?? []
    const updated = [...nodes].map(node => {
      if (!node.id) node.id = `${titleID}-section-${nextSectionID.current++}`
      return {id: node.id, title: node.textContent?.replace(/^\d+\.\s*/, '') ?? ''}
    })
    setSections(current => JSON.stringify(current) === JSON.stringify(updated) ? current : updated)
  }, [children, titleID])
  useEffect(() => {
    if (!sections.length) return
    let frame = 0
    const update = () => {
      frame = 0
      const threshold = Math.min(160, window.innerHeight / 4)
      let current = sections[0].id
      for (const section of sections) {
        const node = document.getElementById(section.id)
        if (node && node.getBoundingClientRect().top <= threshold) current = section.id
      }
      setActiveSection(current)
    }
    const schedule = () => {
      if (!frame) frame = window.requestAnimationFrame(update)
    }
    update()
    window.addEventListener('scroll', schedule, {passive: true})
    window.addEventListener('resize', schedule)
    return () => {
      window.cancelAnimationFrame(frame)
      window.removeEventListener('scroll', schedule)
      window.removeEventListener('resize', schedule)
    }
  }, [sections])
  const page = <section className={`editor-page ${className}`} aria-labelledby={titleID}>
    <header className="editor-page-header">
      {onClose && <ReturnButton onClick={onClose} />}
      <h2 id={titleID} ref={heading} tabIndex={-1}>{title}</h2>
    </header>
    <div className={`editor-page-layout${sections.length ? ' has-sections' : ''}`}>
      {sections.length > 0 && <nav className="editor-sections" aria-label="Form sections">{sections.map((section, index) => <button key={section.id} type="button" aria-current={(activeSection || sections[0]?.id) === section.id ? 'location' : undefined} onClick={() => {
        setActiveSection(section.id)
        const node = document.getElementById(section.id)
        if (node) {
          node.tabIndex = -1
          node.scrollIntoView({block: 'start', behavior: 'instant'})
          node.focus({preventScroll: true})
        }
      }}><span aria-hidden="true">{index + 1}</span>{section.title}</button>)}</nav>}
      <div className="editor-page-body" ref={content}>{children}</div>
    </div>
  </section>
  return container ? createPortal(page, container) : page
}
