import type {SectionID, SidebarSection} from '../navigation'
import type {View} from '../types'

const glyphs: Record<SectionID, React.ReactNode> = {
  home: <path d="M4 11 12 4l8 7v9h-5v-6H9v6H4z" />,
  platform: <><rect x="4" y="4" width="16" height="6" rx="1.5" /><rect x="4" y="14" width="16" height="6" rx="1.5" /><path d="M8 7h.01M8 17h.01" /></>,
  lab: <path d="M9 3h6m-5 0v6l-5 9a2 2 0 0 0 1.8 3h10.4a2 2 0 0 0 1.8-3l-5-9V3M7.5 15h9" />,
  team: <><circle cx="9" cy="8" r="3" /><path d="M3.5 19c.6-3.4 2.6-5 5.5-5s4.9 1.6 5.5 5M16 5.5a3 3 0 0 1 0 5M18 14c1.6.6 2.6 2.2 3 5" /></>
}

// Sections stay quiet; only the open one lists its pages beneath a thin guide line.
export function SidebarNavigation({sections, view, navigate}: {sections: SidebarSection[]; view: View; navigate: (view: View) => void}) {
  return <nav className="app-nav" aria-label="Main navigation">
    {sections.map(section => {
      const open = section.items.some(item => item.views.includes(view))
      const single = section.items.length === 1
      return <div className={`app-nav-section${open ? ' is-open' : ''}${single ? ' is-single' : ''}`} key={section.id}>
        <button type="button" className="app-nav-section-link" aria-current={single && open ? 'page' : undefined} onClick={() => navigate(open ? view : section.items[0].view)}>
          <svg className="app-nav-glyph" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{glyphs[section.id]}</svg>
          <span>{section.label}</span>
        </button>
        {open && !single && <ul className="app-nav-pages">
          {section.items.map(item => {
            const current = item.views.includes(view)
            return <li key={item.view}><button type="button" className={`app-nav-link${current ? ' active' : ''}`} aria-current={current ? 'page' : undefined} onClick={() => navigate(item.view)}>{item.label}</button></li>
          })}
        </ul>}
      </div>
    })}
  </nav>
}
