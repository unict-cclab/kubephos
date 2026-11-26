import {useEffect, useId, useMemo, useRef, useState, type DragEvent, type KeyboardEvent, type MouseEvent, type PointerEvent, type RefObject} from 'react'
import type {ChartExportFormat} from '../../../charts'
import {ErrorFeedback, useErrorState} from '../../ToastRegion'
import {FigureHostContext} from '../chartKit'
import {downloadFormats} from '../DownloadMenu'
import {fontStacks, type FigureSettings} from '../figureSettings'
import type {OverviewGroup} from '../overview'
import {anchorAtButton, ContextMenu, type MenuAnchor, type MenuItem} from './ContextMenu'
import {EyeIcon} from './icons'
import {availableKinds, buildViews, chartContent, exportSvg, imagePage, imageSvg, useReports, type StudioData} from './image'
import {newChartId} from './layout'
import {chartSizes, defaultSize, type ChartItem, type ChartSize} from './resultsLayout'
import './resultCharts.css'

// The results' charts at a glance: every metric by default, then whatever the user keeps
// from KubeStudio (opened with "Create figure" in the page header). The grid has six
// columns and fills its rows densely: a chart takes ⅓, ½, ⅔ or the full width, and the
// others reflow around it (a narrower page shows every chart wider). Drag a chart onto the
// edge of another to move it there, or onto its middle to swap the two (Alt + arrow keys on
// its ⋯ button move it too); drag its bottom-right corner to resize it. Click a chart (or press
// F, or its ⤢ button) for full screen; double-click it (or press Enter) to edit it in KubeStudio;
// right-click it (or Shift+F10, or its ⋯ button) for every action, among them Hide: hidden charts wait,
// collapsed, under "Hidden charts" at the bottom. The rest of the page (summary, tables,
// statistics…) is chosen from Sections in the page header (SectionsMenu).

interface Props {
  items: ChartItem[]
  data: StudioData | undefined
  groups: OverviewGroup[]
  // Page style of the charts not edited in KubeStudio.
  settings: FigureSettings
  loading: boolean
  error: string
  onChange: (items: ChartItem[]) => void
  onEdit: (item: ChartItem) => void
}

const chartsHint = 'Click for full screen, double-click to edit in KubeStudio, right-click for more; drag to move, drag the corner to resize.'
const sizeOf = (item: ChartItem): ChartSize => item.size ?? defaultSize
const sizeLabel = (size: ChartSize) => chartSizes.find(entry => entry.id === size)!.label
// Grid widths (px) below which charts show wider: two per row, then one.
export const gridTiers = {twoPerRow: 1000, onePerRow: 560}
// The sizes a chart can visibly take on a grid this wide (narrower ones look the same as these).
export function sizesFor(width: number): ChartSize[] {
  if (!width || width >= gridTiers.twoPerRow) return chartSizes.map(size => size.id)
  return width >= gridTiers.onePerRow ? ['half', 'full'] : ['full']
}
// The size nearest to a share of the grid's width.
export function snapSize(fraction: number, allowed: ChartSize[]): ChartSize {
  return allowed.reduce((best, size) => Math.abs(chartSizes.find(entry => entry.id === size)!.fraction - fraction) < Math.abs(chartSizes.find(entry => entry.id === best)!.fraction - fraction) ? size : best, allowed[0])
}

export function ResultCharts({items, data, groups, settings, loading, error, onChange, onEdit}: Props) {
  const id = useId()
  const grid = useRef<HTMLOListElement>(null)
  const [undo, setUndo] = useState<{text: string; revert: () => void}>()
  const [dragging, setDragging] = useState<string>()
  // Where the dragged chart lands: before the chart at `index` (visible.length: at the end), or in its place, swapping the two.
  const [target, setTarget] = useState<{index: number; swap: boolean}>()
  const [status, setStatus] = useState('')
  // The size shown while a chart's corner is dragged, saved when it is let go.
  const [resizing, setResizing] = useState<{id: string; size: ChartSize}>()
  const [fullscreen, setFullscreen] = useState<string>()
  const [hiddenOpen, setHiddenOpen] = useState(false)
  const visible = items.filter(item => !item.concealed)
  const concealed = items.filter(item => item.concealed)
  const change = (next: ChartItem[], text?: string) => {
    setUndo(text ? {text, revert: () => onChange(items)} : undefined)
    onChange(next)
  }
  // Moves happen among the visible charts; hidden ones keep their place after them.
  const reorder = (next: ChartItem[]) => change([...next, ...concealed])
  const move = (itemId: string, to: number) => {
    const from = visible.findIndex(item => item.id === itemId)
    const target = Math.max(0, Math.min(visible.length - 1, to))
    if (from < 0 || from === target) return
    const next = [...visible]
    const [item] = next.splice(from, 1)
    next.splice(target, 0, item)
    reorder(next)
    setStatus(`${item.name} moved to position ${target + 1} of ${visible.length}.`)
  }
  const swap = (itemId: string, index: number) => {
    const from = visible.findIndex(item => item.id === itemId)
    if (from < 0 || from === index) return
    const next = [...visible]
    ;[next[from], next[index]] = [next[index], next[from]]
    reorder(next)
    setStatus(`${visible[from].name} swapped with ${visible[index].name}.`)
  }
  const endDrag = () => {setDragging(undefined); setTarget(undefined)}
  const drop = () => {
    if (dragging && target) {
      const from = visible.findIndex(item => item.id === dragging)
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
    const next = event.clientX < box.left + edge ? {index, swap: false} : event.clientX > box.right - edge ? {index: index + 1, swap: false} : visible[index].id === dragging ? undefined : {index, swap: true}
    if (next?.index !== target?.index || next?.swap !== target?.swap) setTarget(next)
  }
  const setConcealed = (item: ChartItem, hide: boolean) => {
    change(items.map(entry => entry.id === item.id ? {...entry, concealed: hide || undefined} : entry), hide ? `“${item.name}” hidden. It is listed under Hidden charts.` : undefined)
    setStatus(hide ? `${item.name} hidden.` : `${item.name} shown again.`)
  }
  const actions: ItemActions = {
    edit: onEdit,
    fullscreen: item => setFullscreen(item.id),
    duplicate: item => {const index = items.indexOf(item); change([...items.slice(0, index + 1), {...structuredClone(item), id: newChartId(), name: `${item.name} (copy)`}, ...items.slice(index + 1)])},
    hide: item => setConcealed(item, true),
    rename: (item, name) => {
      change(items.map(entry => entry.id === item.id ? {...entry, name} : entry))
      setStatus(`Renamed to ${name}.`)
    },
    resize: (item, size, done) => {
      if (!done) {setResizing(current => current?.id === item.id && current.size === size ? current : {id: item.id, size}); return}
      setResizing(undefined)
      if (size === sizeOf(item)) return
      change(items.map(entry => entry.id === item.id ? {...entry, size: size === defaultSize ? undefined : size} : entry))
      setStatus(`${item.name} is now ${sizeLabel(size).toLowerCase()}.`)
    },
    move,
    dragStart: itemId => setDragging(itemId),
    dragEnd: endDrag
  }
  const opened = fullscreen ? visible.find(item => item.id === fullscreen) : undefined
  return <section className="report-figure result-charts" aria-labelledby={`${id}-title`} id="results-charts">
    <header className="report-head">
      <h3 id={`${id}-title`}>Charts</h3>
      {visible.length > 0 && <p className="result-charts-hint" id={`${id}-hint`} title={chartsHint}>{chartsHint}</p>}
    </header>
    <span className="visually-hidden" role="status">{status}</span>
    {undo && <p className="result-charts-notice" role="status">{undo.text} <button type="button" className="link-button" onClick={() => {undo.revert(); setUndo(undefined)}}>Undo</button></p>}
    <ErrorFeedback message={error} />
    {loading ? <p className="result-charts-empty" role="status">Loading your charts…</p>
      : !visible.length ? <div className="result-charts-empty"><p>{concealed.length ? 'Every chart is hidden: show them again from Hidden charts below' : 'No charts here'}. Create one with Create figure at the top of the page.</p></div>
      : <ol ref={grid} className="result-chart-grid" data-resizing={resizing ? true : undefined} onDragOver={event => {if (dragging) event.preventDefault()}}>
        {visible.map((item, index) => <li key={item.id} data-size={resizing?.id === item.id ? resizing.size : sizeOf(item)} data-drop-before={target && !target.swap && target.index === index || undefined}
          data-drop-after={target && !target.swap && target.index === visible.length && index === visible.length - 1 || undefined} data-drop-swap={target?.swap && target.index === index || undefined}
          onDragOver={event => dragOver(event, index)} onDrop={event => {event.preventDefault(); drop()}}>
          <ChartCard item={item} position={index} grid={grid} hint={`${id}-hint`} data={data} groups={groups} settings={item.settings ?? settings} dragging={dragging === item.id} resizing={resizing?.id === item.id} actions={actions} />
        </li>)}
      </ol>}
    {!loading && concealed.length > 0 && <div className="result-charts-hidden">
      <button type="button" className="result-charts-hidden-toggle" aria-expanded={hiddenOpen} aria-controls={`${id}-hidden`} onClick={() => setHiddenOpen(value => !value)}>
        <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><path d="m4.5 3 3 3-3 3" /></svg>Hidden charts ({concealed.length})
      </button>
      {hiddenOpen && <ul id={`${id}-hidden`} className="result-charts-hidden-list">
        {concealed.map(item => <li key={item.id}>
          <span>{item.name}</span>
          <button type="button" className="button secondary small" aria-label={`Show ${item.name}`} title="Show it in the grid again" onClick={() => setConcealed(item, false)}><EyeIcon />Show</button>
        </li>)}
        {concealed.length > 1 && <li className="result-charts-hidden-all"><button type="button" className="link-button" onClick={() => {change(items.map(entry => ({...entry, concealed: undefined}))); setStatus('Every chart shown again.')}}>Show all</button></li>}
      </ul>}
    </div>}
    {opened && <ChartFullscreen item={opened} data={data} groups={groups} settings={opened.settings ?? settings} onClose={() => setFullscreen(undefined)} onEdit={() => {setFullscreen(undefined); onEdit(opened)}} />}
  </section>
}

interface ItemActions {
  edit: (item: ChartItem) => void
  fullscreen: (item: ChartItem) => void
  duplicate: (item: ChartItem) => void
  hide: (item: ChartItem) => void
  rename: (item: ChartItem, name: string) => void
  // done: false while dragging (a preview), true when let go.
  resize: (item: ChartItem, size: ChartSize, done: boolean) => void
  move: (itemId: string, to: number) => void
  dragStart: (itemId: string) => void
  dragEnd: () => void
}

// The drawn image of a kept chart: one chart, or a lettered page of up to four.
function useChartImage(item: ChartItem, data: StudioData | undefined, groups: OverviewGroup[], settings: FigureSettings, prefix: string) {
  const {reports, hosts} = useReports(item.charts)
  const views = useMemo(() => buildViews({image: item, data, groups, settings, available: availableKinds(data)}), [item, data, groups, settings])
  const rendered = views.map(view => chartContent(view, {data, groups, hidden: item.hidden, idPrefix: `${prefix}${item.id.replaceAll(/[^a-zA-Z0-9]/g, '-')}-`}))
  const isPublication = (index: number) => Boolean(rendered[index]?.[1]) && views[index].kind !== 'placement'
  const page = imagePage(views, reports, settings, view => isPublication(view.index))
  const image = (ref?: RefObject<HTMLDivElement | null>, props: {className?: string; onDoubleClick?: () => void} = {}) => <div className={`result-chart-image ${props.className ?? ''}`.trim()} ref={ref} onDoubleClick={props.onDoubleClick} style={page.single ? undefined : {aspectRatio: `${page.width} / ${page.height}`}}>
    {views.map(view => {
      const cell = page.cellOf(view)
      return <div key={view.chart.id} className="result-chart-cell" data-chart={view.chart.id} style={page.single ? undefined : {left: `${cell.x / page.width * 100}%`, top: `${cell.y / page.height * 100}%`, width: `${cell.width / page.width * 100}%`}}>
        {view.letter && <span className="result-chart-letter" aria-hidden="true" style={{fontFamily: fontStacks[settings.font]}}>{view.letter}</span>}
        <FigureHostContext.Provider value={hosts[view.chart.id]}>{rendered[view.index][0]}</FigureHostContext.Provider>
      </div>
    })}
  </div>
  return {views, page, image}
}

// Within a card these keep their own clicks (legend toggles are buttons); the rest of the card opens the full screen.
const interactive = 'button, a, input, select, textarea, label, summary, [role="button"], [role="menu"], [role="menuitem"], [role="menuitemcheckbox"], [role="checkbox"], [role="switch"]'
export const clickDelay = 250

const ExpandIcon = () => <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M14 4h6v6M10 20H4v-6M20 4l-7 7M4 20l7-7" /></svg>

function ChartCard({item, position, grid, hint, data, groups, settings, dragging, resizing, actions}: {item: ChartItem; position: number; grid: RefObject<HTMLOListElement | null>; hint: string; data: StudioData | undefined; groups: OverviewGroup[]; settings: FigureSettings; dragging: boolean; resizing: boolean; actions: ItemActions}) {
  const image = useRef<HTMLDivElement>(null)
  // Set while the corner handle is pressed: the card must not start a drag-and-drop move then.
  const pressingHandle = useRef(false)
  const [menu, setMenu] = useState<MenuAnchor>()
  const [renaming, setRenaming] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useErrorState()
  const {views, page, image: drawImage} = useChartImage(item, data, groups, settings, '')
  const size = sizeOf(item)
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
  const menuItems: MenuItem[] = [
    {label: 'Full screen', hint: 'Click the chart, or F', glyph: <ExpandIcon />, run: () => actions.fullscreen(item)},
    {label: 'Edit in KubeStudio', hint: 'Double-click the chart, or Enter', icon: 'chart', run: () => actions.edit(item)},
    {label: 'Rename', icon: 'edit', run: () => setRenaming(true)},
    {label: 'Duplicate', icon: 'create', run: () => actions.duplicate(item)},
    ...downloadFormats.map((format): MenuItem => ({label: `Download ${format.label}`, icon: 'download', separated: format.format === 'pdf', run: () => void download(format.format)})),
    {label: 'Hide', hint: 'Listed under Hidden charts', glyph: <EyeIcon off />, separated: true, run: () => actions.hide(item)}
  ]
  // Alt + arrow keys on the ⋯ button: the keyboard way to move a chart.
  const onMoveKey = (event: KeyboardEvent<HTMLButtonElement>) => {
    const by = !event.altKey ? 0 : event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1 : event.key === 'ArrowRight' || event.key === 'ArrowDown' ? 1 : 0
    if (!by) return
    event.preventDefault()
    actions.move(item.id, position + by)
  }
  // The menu at the pointer, or (keyboard) under the card's top-right corner.
  const openMenu = (event: {clientX: number; clientY: number; currentTarget: Element}) => {
    if (event.clientX || event.clientY) setMenu({x: event.clientX, y: event.clientY})
    else {const box = event.currentTarget.getBoundingClientRect(); setMenu({x: box.right, y: box.top + 8, fromButton: true})}
  }
  // On the focused card itself: Enter edits it, F shows it full screen, Shift+F10 or the menu key opens its menu.
  const onCardKey = (event: KeyboardEvent<HTMLElement>) => {
    if (event.target !== event.currentTarget || event.altKey || event.ctrlKey || event.metaKey) return
    if (event.key === 'ContextMenu' || (event.shiftKey && event.key === 'F10')) {event.preventDefault(); openMenu({clientX: 0, clientY: 0, currentTarget: event.currentTarget})}
    else if (event.key === 'Enter') {event.preventDefault(); actions.edit(item)}
    else if (event.key === 'f' || event.key === 'F') {event.preventDefault(); actions.fullscreen(item)}
  }
  const rename = (value: string) => {
    setRenaming(false)
    const name = value.trim()
    if (name && name !== item.name) actions.rename(item, name)
  }
  const allowed = () => sizesFor(grid.current?.clientWidth ?? 0)
  // Drag the corner: the chart snaps to the size nearest to the pointer, and the others reflow live.
  const onResizeStart = (event: PointerEvent<HTMLButtonElement>) => {
    const cell = event.currentTarget.closest('li')
    const list = grid.current
    if (!cell || !list || event.button !== 0) return
    event.preventDefault()
    event.stopPropagation()
    pressingHandle.current = true
    const handle = event.currentTarget
    const steps = allowed()
    const startX = event.clientX
    const startWidth = cell.getBoundingClientRect().width
    const sizeAt = (x: number) => snapSize((startWidth + x - startX) / Math.max(1, list.clientWidth), steps)
    const move = (moved: globalThis.PointerEvent) => actions.resize(item, sizeAt(moved.clientX), false)
    const end = (ended: globalThis.PointerEvent) => {
      pressingHandle.current = false
      handle.removeEventListener('pointermove', move)
      handle.removeEventListener('pointerup', end)
      handle.removeEventListener('pointercancel', end)
      actions.resize(item, ended.type === 'pointerup' ? sizeAt(ended.clientX) : size, true)
    }
    try {handle.setPointerCapture?.(event.pointerId)} catch {/* the pointer is already gone */}
    handle.addEventListener('pointermove', move)
    handle.addEventListener('pointerup', end)
    handle.addEventListener('pointercancel', end)
  }
  // Arrow keys on the corner step through the sizes this grid shows.
  const onResizeKey = (event: KeyboardEvent<HTMLButtonElement>) => {
    const by = event.key === 'ArrowLeft' || event.key === 'ArrowDown' ? -1 : event.key === 'ArrowRight' || event.key === 'ArrowUp' ? 1 : 0
    if (!by) return
    event.preventDefault()
    const steps = allowed()
    const current = steps.indexOf(snapSize(chartSizes.find(entry => entry.id === size)!.fraction, steps))
    actions.resize(item, steps[Math.max(0, Math.min(steps.length - 1, current + by))], true)
  }
  const stop = (event: MouseEvent) => event.stopPropagation()
  // One click opens the full screen, two open KubeStudio: the single click waits to see whether a
  // second one follows, so a double click never flashes the overlay. Clicks on the card's own
  // controls (buttons, links, fields, legend toggles) are theirs.
  const pending = useRef<ReturnType<typeof setTimeout>>(undefined)
  useEffect(() => () => clearTimeout(pending.current), [])
  const onCardClick = (event: MouseEvent<HTMLElement>) => {
    const target = event.target instanceof Element ? event.target : null
    if (renaming || event.button !== 0 || target?.closest(interactive)) return
    clearTimeout(pending.current)
    if (event.detail > 1) return
    pending.current = setTimeout(() => actions.fullscreen(item), clickDelay)
  }
  const onCardDoubleClick = (event: MouseEvent<HTMLElement>) => {
    const target = event.target instanceof Element ? event.target : null
    clearTimeout(pending.current)
    if (!renaming && !target?.closest(interactive)) actions.edit(item)
  }
  return <figure className="result-chart" data-dragging={dragging || undefined} data-resizing={resizing || undefined} draggable={!renaming && !resizing} tabIndex={0} aria-label={item.name} aria-describedby={hint} aria-keyshortcuts="Enter F Shift+F10"
    onKeyDown={onCardKey}
    onDragStart={event => {
      if (pressingHandle.current) {event.preventDefault(); return}
      event.dataTransfer.effectAllowed = 'move'; event.dataTransfer.setData('text/plain', item.id); actions.dragStart(item.id)
    }} onDragEnd={actions.dragEnd}
    onContextMenu={(event: MouseEvent<HTMLElement>) => {event.preventDefault(); clearTimeout(pending.current); openMenu(event)}}
    onClick={onCardClick} onDoubleClick={onCardDoubleClick}>
    <figcaption className="result-chart-head">
      {renaming
        ? <input className="result-chart-name" aria-label="Chart name" defaultValue={item.name} maxLength={120} autoFocus onFocus={event => event.currentTarget.select()} onDoubleClick={stop}
          onBlur={event => rename(event.currentTarget.value)}
          onKeyDown={event => {event.stopPropagation(); if (event.key === 'Enter') event.currentTarget.blur(); else if (event.key === 'Escape') {event.preventDefault(); setRenaming(false)}}} />
        : <strong onDoubleClick={event => {event.stopPropagation(); setRenaming(true)}} title="Double-click to rename">{item.name}</strong>}
      <button type="button" className="icon-button" aria-label={`Full screen: ${item.name}`} title="Full screen (F, or click the chart)" onClick={() => actions.fullscreen(item)} onDoubleClick={stop}><ExpandIcon /></button>
      <button type="button" className="icon-button" aria-label={`Actions for ${item.name}`} aria-haspopup="menu" aria-expanded={Boolean(menu)} aria-keyshortcuts="Alt+ArrowLeft Alt+ArrowRight" title="Actions (Alt + arrow keys move the chart)" disabled={busy} onKeyDown={onMoveKey} onDoubleClick={stop} onClick={event => setMenu(anchorAtButton(event.currentTarget))}>
        <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true"><circle cx="3" cy="8" r="1.4" /><circle cx="8" cy="8" r="1.4" /><circle cx="13" cy="8" r="1.4" /></svg>
      </button>
    </figcaption>
    {drawImage(image)}
    {item.caption && <p className="figure-caption">{item.caption}</p>}
    <ErrorFeedback message={error} />
    <button type="button" className="result-chart-resize" aria-label={`Resize ${item.name}: drag it, or use the arrow keys (now ${sizeLabel(size)})`} title="Drag to make the chart wider or narrower"
      onPointerDown={onResizeStart} onKeyDown={onResizeKey} onDoubleClick={stop} onClick={stop}>
      <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><path d="M11 4 4 11M11 8l-3 3" /></svg>
    </button>
    {menu && <ContextMenu at={menu} label={item.name} items={menuItems} onClose={() => setMenu(undefined)} />}
  </figure>
}

// One chart over the whole window, drawn larger: Esc or Close leaves, Edit opens KubeStudio.
function ChartFullscreen({item, data, groups, settings, onClose, onEdit}: {item: ChartItem; data: StudioData | undefined; groups: OverviewGroup[]; settings: FigureSettings; onClose: () => void; onEdit: () => void}) {
  const id = useId()
  const dialog = useRef<HTMLDialogElement>(null)
  const {image} = useChartImage(item, data, groups, settings, 'fullscreen-')
  useEffect(() => {
    const element = dialog.current
    if (!element) return
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
    if (!element.open) {try {element.showModal()} catch {element.setAttribute('open', '')}}
    element.querySelector<HTMLButtonElement>('.result-fullscreen-close')?.focus({preventScroll: true})
    return () => {if (element.open) element.close(); if (previous?.isConnected) previous.focus({preventScroll: true})}
  }, [])
  return <dialog ref={dialog} className="result-fullscreen" aria-labelledby={`${id}-title`} onCancel={event => {event.preventDefault(); onClose()}}
    onClick={event => {if (event.target === event.currentTarget) onClose()}} onKeyDown={event => {if (event.key === 'Escape') {event.preventDefault(); onClose()}}}>
    <div className="result-fullscreen-body">
      <header className="result-fullscreen-head">
        <h2 id={`${id}-title`}>{item.name}</h2>
        <button type="button" className="button secondary" onClick={onEdit}>Edit in KubeStudio</button>
        <button type="button" className="icon-button result-fullscreen-close" aria-label="Close full screen" title="Close (Esc)" onClick={onClose}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" aria-hidden="true"><path d="m6 6 12 12M6 18 18 6" /></svg>
        </button>
      </header>
      {image(undefined, {className: 'result-fullscreen-image', onDoubleClick: onEdit})}
      {item.caption && <p className="figure-caption">{item.caption}</p>}
    </div>
  </dialog>
}
