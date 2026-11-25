import {useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type MouseEvent} from 'react'
import type {ChartExportFormat} from '../../../charts'
import {ActionIcon, type ActionIconName} from '../../ActionIcon'
import {ErrorFeedback, useErrorState} from '../../ToastRegion'
import {FigureHostContext} from '../chartKit'
import {downloadFormats} from '../DownloadMenu'
import {fontStacks} from '../figureSettings'
import type {OverviewGroup} from '../overview'
import {availableKinds, buildViews, chartContent, exportSvg, imagePage, imageSvg, useReports, type StudioData} from './image'
import {KubeStudioMark} from './KubeStudioMark'
import type {SavedFigure} from './savedFigures'
import './resultFigures.css'

// The figures kept on the results page, made with KubeStudio. Each one opens a menu
// with a right click or its ⋯ button: edit in KubeStudio, download, delete.

interface FigureActions {
  onEdit: (figure: SavedFigure) => void
  onDelete: (figure: SavedFigure) => void
}

export function ResultFigures({figures, data, groups, stored, onAdd, onEdit, onDelete, deleted, onUndo}: FigureActions & {figures: SavedFigure[]; data: StudioData | undefined; groups: OverviewGroup[]; stored: boolean; onAdd: () => void; deleted?: string; onUndo: () => void}) {
  const id = useId()
  return <section className="report-figure result-figures" aria-labelledby={`${id}-title`}>
    <header className="report-head report-head-actions">
      <p className="report-label">Your figures</p>
      <h3 id={`${id}-title`}>Figures made with KubeStudio</h3>
      <p className="report-sub">{figures.length ? 'Right-click a figure, or use its ⋯ button, to edit, download or delete it.' : 'Build a figure in KubeStudio and keep it here, next to the results.'}</p>
      <div className="report-actions"><button type="button" className="button secondary" onClick={onAdd}><KubeStudioMark size={18} className="kubestudio-entry" />Add figure</button></div>
    </header>
    {deleted && <p className="result-figures-notice" role="status">“{deleted}” deleted. <button type="button" className="link-button" onClick={onUndo}>Undo</button></p>}
    {!stored && <p className="validation-item warning">This browser does not keep figures: they last until the page is reloaded.</p>}
    {figures.length > 0 && <ol className="result-figure-grid">
      {figures.map((figure, index) => <li key={figure.id}><FigureItem figure={figure} number={index + 2} data={data} groups={groups} onEdit={onEdit} onDelete={onDelete} /></li>)}
    </ol>}
  </section>
}

function FigureItem({figure, number, data, groups, onEdit, onDelete}: FigureActions & {figure: SavedFigure; number: number; data: StudioData | undefined; groups: OverviewGroup[]}) {
  const image = useRef<HTMLDivElement>(null)
  const [menu, setMenu] = useState<{x: number; y: number; fromButton: boolean}>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useErrorState()
  const {reports, hosts} = useReports(figure.charts)
  const views = useMemo(() => buildViews({image: figure, data, groups, settings: figure.settings, available: availableKinds(data)}), [figure, data, groups])
  const rendered = views.map(view => chartContent(view, {data, groups, hidden: figure.hidden, idPrefix: `${figure.id}-`}))
  const isPublication = (index: number) => Boolean(rendered[index]?.[1]) && views[index].kind !== 'placement'
  const page = imagePage(views, reports, figure.settings, view => isPublication(view.index))
  const download = async (format: ChartExportFormat) => {
    setBusy(true)
    setError('')
    try {
      const svgOf = (view: (typeof views)[number]) => image.current?.querySelector<SVGSVGElement>(`[data-chart="${view.chart.id}"] svg[id]`) ?? null
      const svg = imageSvg(views, page, svgOf, figure.settings, `result-figure-${figure.id}-export`)
      await exportSvg(svg, format, figure.name, {title: figure.name, caption: figure.caption, fileName: `kubephos-${figure.name.normalize('NFKD').replaceAll(/[^a-zA-Z0-9]+/g, '-').replaceAll(/^-+|-+$/g, '').toLowerCase().slice(0, 80) || 'figure'}`, creator: 'KubePhos KubeStudio'})
    } catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not download the figure. Try again.')}
    finally {setBusy(false)}
  }
  const openAt = (event: MouseEvent) => {event.preventDefault(); setMenu({x: event.clientX, y: event.clientY, fromButton: false})}
  const openFromButton = (event: MouseEvent<HTMLButtonElement>) => {const box = event.currentTarget.getBoundingClientRect(); setMenu({x: box.right, y: box.bottom + 4, fromButton: true})}
  return <figure className="result-figure" onContextMenu={openAt} aria-label={`Figure ${number}: ${figure.name}`}>
    <figcaption className="result-figure-head">
      <span><span className="report-label">Figure {number}</span><strong>{figure.name}</strong></span>
      <button type="button" className="icon-button" aria-label={`Actions for figure ${number}`} aria-haspopup="menu" aria-expanded={Boolean(menu)} onClick={openFromButton} disabled={busy}>
        <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true"><circle cx="3" cy="8" r="1.4" /><circle cx="8" cy="8" r="1.4" /><circle cx="13" cy="8" r="1.4" /></svg>
      </button>
    </figcaption>
    <div className="result-figure-image" ref={image} style={page.single ? undefined : {aspectRatio: `${page.width} / ${page.height}`}}>
      {views.map(view => {
        const cell = page.cellOf(view)
        return <div key={view.chart.id} className="result-figure-cell" data-chart={view.chart.id} style={page.single ? undefined : {left: `${cell.x / page.width * 100}%`, top: `${cell.y / page.height * 100}%`, width: `${cell.width / page.width * 100}%`}}>
          {view.letter && <span className="result-figure-letter" aria-hidden="true" style={{fontFamily: fontStacks[figure.settings.font]}}>{view.letter}</span>}
          <FigureHostContext.Provider value={hosts[view.chart.id]}>{rendered[view.index][0]}</FigureHostContext.Provider>
        </div>
      })}
    </div>
    {figure.caption && <p className="figure-caption">{figure.caption}</p>}
    <ErrorFeedback message={error} />
    {menu && <FigureMenu at={menu} label={`Figure ${number}`} onClose={() => setMenu(undefined)} items={[
      {label: 'Edit in KubeStudio', icon: 'edit', run: () => onEdit(figure)},
      ...downloadFormats.map((item): MenuItem => ({label: `Download ${item.label}`, hint: item.hint, icon: 'download', run: () => void download(item.format)})),
      {label: 'Delete figure', icon: 'delete', danger: true, separated: true, run: () => onDelete(figure)}
    ]} />}
  </figure>
}

interface MenuItem {label: string; hint?: string; icon: ActionIconName; danger?: boolean; separated?: boolean; run: () => void}

// A context menu at the pointer (or under the ⋯ button): arrow keys move, Esc closes.
function FigureMenu({at, label, items, onClose}: {at: {x: number; y: number; fromButton: boolean}; label: string; items: MenuItem[]; onClose: () => void}) {
  const root = useRef<HTMLDivElement>(null)
  const [position, setPosition] = useState({left: at.x, top: at.y})
  useLayoutEffect(() => {
    const box = root.current?.getBoundingClientRect()
    if (!box) return
    // Opened from the button the menu hangs to its left; it always stays in the window.
    const left = Math.max(8, Math.min((at.fromButton ? at.x - box.width : at.x), window.innerWidth - box.width - 8))
    const top = Math.max(8, Math.min(at.y, window.innerHeight - box.height - 8))
    setPosition({left, top})
    root.current?.querySelector<HTMLButtonElement>('[role="menuitem"]')?.focus()
  }, [at])
  useEffect(() => {
    const outside = (event: globalThis.MouseEvent) => {if (!root.current?.contains(event.target as Node)) onClose()}
    const away = () => onClose()
    document.addEventListener('mousedown', outside)
    window.addEventListener('scroll', away, true)
    window.addEventListener('resize', away)
    return () => {document.removeEventListener('mousedown', outside); window.removeEventListener('scroll', away, true); window.removeEventListener('resize', away)}
  }, [onClose])
  const onKey = (event: KeyboardEvent<HTMLDivElement>) => {
    const buttons = [...(root.current?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]') ?? [])]
    const index = buttons.indexOf(document.activeElement as HTMLButtonElement)
    if (event.key === 'Escape' || event.key === 'Tab') {event.preventDefault(); onClose()}
    else if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {event.preventDefault(); buttons[(index + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length]?.focus()}
    else if (event.key === 'Home' || event.key === 'End') {event.preventDefault(); buttons[event.key === 'Home' ? 0 : buttons.length - 1]?.focus()}
  }
  return <div ref={root} className="result-figure-menu" role="menu" aria-label={label} style={position} onKeyDown={onKey}>
    {items.map(item => <button key={item.label} type="button" role="menuitem" className={`menu-item${item.danger ? ' danger' : ''}`} data-separated={item.separated || undefined} onClick={() => {onClose(); item.run()}}>
      <ActionIcon name={item.icon} /><span><strong>{item.label}</strong>{item.hint && <small>{item.hint}</small>}</span>
    </button>)}
  </div>
}
