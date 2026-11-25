import {useEffect, useId, useMemo, useRef, useState, type DragEvent, type KeyboardEvent, type MouseEvent, type PointerEvent} from 'react'
import type {ChartExportFormat} from '../../../charts'
import {ErrorFeedback, useErrorState} from '../../ToastRegion'
import {FigureHostContext} from '../chartKit'
import {downloadFormats} from '../DownloadMenu'
import {fontStacks, type FigureSettings} from '../figureSettings'
import type {OverviewGroup} from '../overview'
import {anchorAtButton, ContextMenu, type MenuAnchor, type MenuItem} from './ContextMenu'
import {availableKinds, buildViews, chartContent, exportSvg, imagePage, imageSvg, useReports, type StudioData} from './image'
import {KubeStudioMark} from './KubeStudioMark'
import {newChartId} from './layout'
import {maxSpan, pageSections, type ChartItem, type ChartSpan, type PageSection} from './resultsLayout'
import './resultCharts.css'

// The results' charts at a glance: every metric by default, then whatever the user
// adds with KubeStudio. Drag a chart onto the edge of another to move it there, or onto
// its middle to swap the two (Alt + arrow keys on its ⋯ button move it too); drag its
// bottom-right corner to make it span more or fewer columns; right-click it (or use its ⋯
// button) to edit, rename, duplicate, download or remove it. The Sections menu adds the
// rest of the page (summary, tables, statistics…), which starts hidden.

interface Props {
  items: ChartItem[]
  data: StudioData | undefined
  groups: OverviewGroup[]
  // Page style of the charts not edited in KubeStudio.
  settings: FigureSettings
  loading: boolean
  customized: boolean
  error: string
  // The optional sections these results have, and those shown.
  sections: PageSection[]
  shown: PageSection[]
  onChange: (items: ChartItem[]) => void
  onToggleSection: (id: PageSection, shown: boolean) => void
  // Restores the default page; returns how to undo it.
  onReset: () => () => void
  onAdd: () => void
  onEdit: (item: ChartItem) => void
}

export function ResultCharts({items, data, groups, settings, loading, customized, error, sections, shown, onChange, onToggleSection, onReset, onAdd, onEdit}: Props) {
  const id = useId()
  const [sectionMenu, setSectionMenu] = useState<MenuAnchor>()
  const [undo, setUndo] = useState<{text: string; revert: () => void}>()
  const [dragging, setDragging] = useState<string>()
  // Where the dragged chart lands: before the chart at `index` (items.length: at the end), or in its place, swapping the two.
  const [target, setTarget] = useState<{index: number; swap: boolean}>()
  const [status, setStatus] = useState('')
  // The width shown while a chart's corner is dragged, saved when it is let go.
  const [resizing, setResizing] = useState<{id: string; span: ChartSpan}>()
  const [grid, columns] = useColumns()
  const change = (next: ChartItem[], text?: string) => {
    setUndo(text ? {text, revert: () => onChange(items)} : undefined)
    onChange(next)
  }
  const move = (itemId: string, to: number) => {
    const from = items.findIndex(item => item.id === itemId)
    const target = Math.max(0, Math.min(items.length - 1, to))
    if (from < 0 || from === target) return
    const next = [...items]
    const [item] = next.splice(from, 1)
    next.splice(target, 0, item)
    change(next)
    setStatus(`${item.name} moved to position ${target + 1} of ${items.length}.`)
  }
  const swap = (itemId: string, index: number) => {
    const from = items.findIndex(item => item.id === itemId)
    if (from < 0 || from === index) return
    const next = [...items]
    ;[next[from], next[index]] = [next[index], next[from]]
    change(next)
    setStatus(`${items[from].name} swapped with ${items[index].name}.`)
  }
  const endDrag = () => {setDragging(undefined); setTarget(undefined)}
  const drop = () => {
    if (dragging && target) {
      const from = items.findIndex(item => item.id === dragging)
      if (target.swap) swap(dragging, target.index)
      else move(dragging, from < target.index ? target.index - 1 : target.index)
    }
    endDrag()
  }
  // The outer quarters of a chart insert before or after it; the middle swaps.
  const dragOver = (event: DragEvent<HTMLLIElement>, index: number) => {
    if (!dragging) return
    event.preventDefault()
    const box = event.currentTarget.getBoundingClientRect()
    const edge = box.width / 4
    const next = event.clientX < box.left + edge ? {index, swap: false} : event.clientX > box.right - edge ? {index: index + 1, swap: false} : items[index].id === dragging ? undefined : {index, swap: true}
    if (next?.index !== target?.index || next?.swap !== target?.swap) setTarget(next)
  }
  const actions: ItemActions = {
    edit: onEdit,
    duplicate: item => {const index = items.indexOf(item); change([...items.slice(0, index + 1), {...structuredClone(item), id: newChartId(), name: `${item.name} (copy)`}, ...items.slice(index + 1)])},
    remove: item => change(items.filter(entry => entry.id !== item.id), `“${item.name}” removed.`),
    rename: (item, name) => {
      change(items.map(entry => entry.id === item.id ? {...entry, name} : entry))
      setStatus(`Renamed to ${name}.`)
    },
    resize: (item, span, done) => {
      if (!done) {setResizing({id: item.id, span}); return}
      setResizing(undefined)
      if (span === (item.span ?? 1)) return
      change(items.map(entry => entry.id === item.id ? {...entry, span: span > 1 ? span : undefined} : entry))
      setStatus(`${item.name} now spans ${span} column${span > 1 ? 's' : ''}.`)
    },
    move,
    dragStart: itemId => setDragging(itemId),
    dragEnd: endDrag
  }
  return <section className="report-figure result-charts" aria-labelledby={`${id}-title`} id="results-charts">
    <header className="report-head report-head-actions">
      <h3 id={`${id}-title`}>Charts</h3>
      <div className="report-actions">
        {customized && <button type="button" className="button secondary" onClick={() => setUndo({text: 'Default page restored.', revert: onReset()})}>Reset</button>}
        {sections.length > 0 && <button type="button" className="button secondary" aria-haspopup="menu" aria-expanded={Boolean(sectionMenu)} onClick={event => setSectionMenu(anchorAtButton(event.currentTarget))}>Sections</button>}
        <button type="button" className="button secondary" onClick={onAdd}><KubeStudioMark size={18} className="kubestudio-entry" />Add chart</button>
      </div>
      {sectionMenu && <ContextMenu at={sectionMenu} label="Sections" onClose={() => setSectionMenu(undefined)}
        items={pageSections.filter(section => sections.includes(section.id)).map((section): MenuItem => ({label: section.label, checked: shown.includes(section.id), run: () => onToggleSection(section.id, !shown.includes(section.id))}))} />}
    </header>
    <span className="visually-hidden" role="status">{status}</span>
    {undo && <p className="result-charts-notice" role="status">{undo.text} <button type="button" className="link-button" onClick={() => {undo.revert(); setUndo(undefined)}}>Undo</button></p>}
    <ErrorFeedback message={error} />
    {loading ? <p className="result-charts-empty" role="status">Loading your charts…</p>
      : !items.length ? <div className="result-charts-empty"><p>No charts here. Add one with KubeStudio{customized ? ', or reset to the default charts' : ''}.</p></div>
      : <ol ref={grid} className="result-chart-grid" style={{gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))`}} onDragOver={event => {if (dragging) event.preventDefault()}}>
        {items.map((item, index) => <li key={item.id} style={{gridColumn: `span ${Math.min(columns, resizing?.id === item.id ? resizing.span : item.span ?? 1)}`}} data-drop-before={target && !target.swap && target.index === index || undefined}
          data-drop-after={target && !target.swap && target.index === items.length && index === items.length - 1 || undefined} data-drop-swap={target?.swap && target.index === index || undefined}
          onDragOver={event => dragOver(event, index)} onDrop={event => {event.preventDefault(); drop()}}>
          <ChartCard item={item} position={index} columns={columns} data={data} groups={groups} settings={item.settings ?? settings} dragging={dragging === item.id} actions={actions} />
        </li>)}
      </ol>}
  </section>
}

const clampSpan = (span: number, columns: number = maxSpan) => Math.max(1, Math.min(columns, span)) as ChartSpan

// Columns of the grid: one to three, from its width. A chart never spans more than there
// are, so the page never scrolls sideways. Without a measured width (tests), three.
function useColumns() {
  const grid = useRef<HTMLOListElement>(null)
  const [columns, setColumns] = useState<number>(maxSpan)
  useEffect(() => {
    const element = grid.current
    if (!element || typeof ResizeObserver === 'undefined') return
    const measure = () => {const width = element.clientWidth; setColumns(!width ? maxSpan : width >= 1100 ? 3 : width >= 720 ? 2 : 1)}
    const observer = new ResizeObserver(measure)
    observer.observe(element)
    measure()
    return () => observer.disconnect()
  })
  return [grid, columns] as const
}

interface ItemActions {
  edit: (item: ChartItem) => void
  duplicate: (item: ChartItem) => void
  remove: (item: ChartItem) => void
  rename: (item: ChartItem, name: string) => void
  // done: false while dragging (a preview), true when let go.
  resize: (item: ChartItem, span: ChartSpan, done: boolean) => void
  move: (itemId: string, to: number) => void
  dragStart: (itemId: string) => void
  dragEnd: () => void
}

function ChartCard({item, position, columns, data, groups, settings, dragging, actions}: {item: ChartItem; position: number; columns: number; data: StudioData | undefined; groups: OverviewGroup[]; settings: FigureSettings; dragging: boolean; actions: ItemActions}) {
  const image = useRef<HTMLDivElement>(null)
  const [menu, setMenu] = useState<MenuAnchor>()
  const [renaming, setRenaming] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useErrorState()
  const {reports, hosts} = useReports(item.charts)
  const views = useMemo(() => buildViews({image: item, data, groups, settings, available: availableKinds(data)}), [item, data, groups, settings])
  const rendered = views.map(view => chartContent(view, {data, groups, hidden: item.hidden, idPrefix: `${item.id.replaceAll(/[^a-zA-Z0-9]/g, '-')}-`}))
  const isPublication = (index: number) => Boolean(rendered[index]?.[1]) && views[index].kind !== 'placement'
  const page = imagePage(views, reports, settings, view => isPublication(view.index))
  const download = async (format: ChartExportFormat) => {
    setBusy(true)
    setError('')
    try {
      const svgOf = (view: (typeof views)[number]) => image.current?.querySelector<SVGSVGElement>(`[data-chart="${view.chart.id}"] svg[id]`) ?? null
      const svg = imageSvg(views, page, svgOf, settings, `result-chart-${newChartId()}`)
      const file = item.name.normalize('NFKD').replaceAll(/[^a-zA-Z0-9]+/g, '-').replaceAll(/^-+|-+$/g, '').toLowerCase().slice(0, 80) || 'chart'
      await exportSvg(svg, format, item.name, {title: item.name, caption: item.caption, fileName: `kubephos-${file}`, creator: 'KubePhos KubeStudio'})
    } catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not download the chart. Try again.')}
    finally {setBusy(false)}
  }
  const items: MenuItem[] = [
    {label: 'Edit in KubeStudio', icon: 'chart', run: () => actions.edit(item)},
    {label: 'Rename', icon: 'edit', run: () => setRenaming(true)},
    {label: 'Duplicate', icon: 'create', run: () => actions.duplicate(item)},
    ...downloadFormats.map((format): MenuItem => ({label: `Download ${format.label}`, icon: 'download', separated: format.format === 'pdf', run: () => void download(format.format)})),
    {label: 'Remove from this page', icon: 'delete', danger: true, separated: true, run: () => actions.remove(item)}
  ]
  // Alt + arrow keys on the ⋯ button: the keyboard way to move a chart.
  const onMoveKey = (event: KeyboardEvent<HTMLButtonElement>) => {
    const by = !event.altKey ? 0 : event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1 : event.key === 'ArrowRight' || event.key === 'ArrowDown' ? 1 : 0
    if (!by) return
    event.preventDefault()
    actions.move(item.id, position + by)
  }
  const rename = (value: string) => {
    setRenaming(false)
    const name = value.trim()
    if (name && name !== item.name) actions.rename(item, name)
  }
  // Drag the corner: the chart snaps to the number of columns nearest to the pointer.
  const onResizeStart = (event: PointerEvent<HTMLButtonElement>) => {
    const cell = event.currentTarget.closest('li')
    if (!cell?.parentElement || event.button !== 0) return
    event.preventDefault()
    const handle = event.currentTarget
    const gap = parseFloat(getComputedStyle(cell.parentElement).columnGap) || 0
    const column = (cell.parentElement.clientWidth - gap * (columns - 1)) / columns
    const startX = event.clientX
    const startWidth = cell.getBoundingClientRect().width
    const spanAt = (x: number) => clampSpan(Math.round((startWidth + x - startX + gap) / (column + gap)), columns)
    const move = (moved: globalThis.PointerEvent) => actions.resize(item, spanAt(moved.clientX), false)
    const end = (ended: globalThis.PointerEvent) => {
      handle.removeEventListener('pointermove', move)
      handle.removeEventListener('pointerup', end)
      handle.removeEventListener('pointercancel', end)
      actions.resize(item, ended.type === 'pointerup' ? spanAt(ended.clientX) : item.span ?? 1, true)
    }
    handle.setPointerCapture?.(event.pointerId)
    handle.addEventListener('pointermove', move)
    handle.addEventListener('pointerup', end)
    handle.addEventListener('pointercancel', end)
  }
  const onResizeKey = (event: KeyboardEvent<HTMLButtonElement>) => {
    const by = event.key === 'ArrowLeft' ? -1 : event.key === 'ArrowRight' ? 1 : 0
    if (!by) return
    event.preventDefault()
    actions.resize(item, clampSpan(Math.min(columns, item.span ?? 1) + by, columns), true)
  }
  return <figure className="result-chart" data-dragging={dragging || undefined} draggable={!renaming}
    onDragStart={event => {event.dataTransfer.effectAllowed = 'move'; event.dataTransfer.setData('text/plain', item.id); actions.dragStart(item.id)}} onDragEnd={actions.dragEnd} onContextMenu={(event: MouseEvent) => {event.preventDefault(); setMenu({x: event.clientX, y: event.clientY})}} aria-label={item.name}>
    <figcaption className="result-chart-head">
      {renaming
        ? <input className="result-chart-name" aria-label="Chart name" defaultValue={item.name} maxLength={120} autoFocus onFocus={event => event.currentTarget.select()}
          onBlur={event => rename(event.currentTarget.value)}
          onKeyDown={event => {if (event.key === 'Enter') event.currentTarget.blur(); else if (event.key === 'Escape') {event.preventDefault(); setRenaming(false)}}} />
        : <strong onDoubleClick={() => setRenaming(true)} title="Double-click to rename">{item.name}</strong>}
      <button type="button" className="icon-button" aria-label={`Actions for ${item.name}`} aria-haspopup="menu" aria-expanded={Boolean(menu)} aria-keyshortcuts="Alt+ArrowLeft Alt+ArrowRight" title="Actions (Alt + arrow keys move the chart)" disabled={busy} onKeyDown={onMoveKey} onClick={event => setMenu(anchorAtButton(event.currentTarget))}>
        <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true"><circle cx="3" cy="8" r="1.4" /><circle cx="8" cy="8" r="1.4" /><circle cx="13" cy="8" r="1.4" /></svg>
      </button>
    </figcaption>
    <div className="result-chart-image" ref={image} onDoubleClick={() => actions.edit(item)} title="Double-click to edit in KubeStudio" style={page.single ? undefined : {aspectRatio: `${page.width} / ${page.height}`}}>
      {views.map(view => {
        const cell = page.cellOf(view)
        return <div key={view.chart.id} className="result-chart-cell" data-chart={view.chart.id} style={page.single ? undefined : {left: `${cell.x / page.width * 100}%`, top: `${cell.y / page.height * 100}%`, width: `${cell.width / page.width * 100}%`}}>
          {view.letter && <span className="result-chart-letter" aria-hidden="true" style={{fontFamily: fontStacks[settings.font]}}>{view.letter}</span>}
          <FigureHostContext.Provider value={hosts[view.chart.id]}>{rendered[view.index][0]}</FigureHostContext.Provider>
        </div>
      })}
    </div>
    {item.caption && <p className="figure-caption">{item.caption}</p>}
    <ErrorFeedback message={error} />
    {columns > 1 && <button type="button" className="result-chart-resize" aria-label={`Resize ${item.name}: drag it, or use the left and right arrow keys (${Math.min(columns, item.span ?? 1)} of ${columns} columns)`} title="Drag to make the chart wider or narrower"
      onPointerDown={onResizeStart} onKeyDown={onResizeKey}>
      <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><path d="M11 4 4 11M11 8l-3 3" /></svg>
    </button>}
    {menu && <ContextMenu at={menu} label={item.name} items={items} onClose={() => setMenu(undefined)} />}
  </figure>
}
