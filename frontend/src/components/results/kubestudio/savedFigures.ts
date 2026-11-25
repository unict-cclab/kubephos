import {useState} from 'react'
import {defaultFigureSettings, type FigureSettings} from '../figureSettings'
import type {ImageState} from './image'
import {newChartId} from './layout'
import {storedCharts} from './memory'

// Figures the user keeps on an experiment's results page: KubeStudio images with
// their own page style, per experiment, in this browser (localStorage).

export interface SavedFigure extends ImageState {
  id: string
  name: string
  caption: string
  settings: FigureSettings
}

const prefix = 'kubephos.resultFigures.v1:'

function storedFigure(value: unknown): SavedFigure[] {
  const item = value as Partial<SavedFigure> | null
  if (!item || typeof item !== 'object') return []
  const charts = storedCharts(item.charts)
  if (!charts.length) return []
  return [{
    id: typeof item.id === 'string' ? item.id : newChartId(), name: typeof item.name === 'string' ? item.name : 'Figure', caption: typeof item.caption === 'string' ? item.caption : '',
    charts, layout: item.layout, sharedLegend: item.sharedLegend === true, hidden: Array.isArray(item.hidden) ? item.hidden.filter(label => typeof label === 'string') : [],
    settings: {...defaultFigureSettings, ...(item.settings && typeof item.settings === 'object' ? item.settings : {})}
  }]
}

function load(key: string): SavedFigure[] {
  try {
    const value = JSON.parse(window.localStorage.getItem(prefix + key) ?? '[]') as unknown
    return Array.isArray(value) ? value.flatMap(storedFigure) : []
  } catch {
    return []
  }
}

// The figures of one experiment, with add, update and remove; `stored` is false
// when the browser refuses to keep them (they then last until the page reloads).
export function useSavedFigures(key: string) {
  const [state, setState] = useState(() => ({key, figures: load(key), stored: true}))
  const current = state.key === key ? state : {key, figures: load(key), stored: true}
  if (current !== state) setState(current)
  const write = (figures: SavedFigure[]) => {
    let stored = true
    try {window.localStorage.setItem(prefix + key, JSON.stringify(figures))} catch {stored = false}
    setState({key, figures, stored})
  }
  return {
    figures: current.figures,
    stored: current.stored,
    add: (figure: Omit<SavedFigure, 'id'>): SavedFigure => {const saved = {...figure, id: newChartId()}; write([...current.figures, saved]); return saved},
    update: (figure: SavedFigure) => write(current.figures.map(item => item.id === figure.id ? figure : item)),
    remove: (id: string) => write(current.figures.filter(item => item.id !== id)),
    restore: (figure: SavedFigure, index: number) => write([...current.figures.slice(0, index), figure, ...current.figures.slice(index)])
  }
}
