import {useEffect, useState} from 'react'
import type {ChartMode} from './chartColor'

const current = (): ChartMode => typeof document !== 'undefined' && document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light'

// Theme of the chart surface; follows the theme toggle (data-theme on <html>).
export function useChartMode(): ChartMode {
  const [mode, setMode] = useState<ChartMode>(current)
  useEffect(() => {
    if (typeof MutationObserver === 'undefined') return
    const observer = new MutationObserver(() => setMode(current()))
    observer.observe(document.documentElement, {attributes: true, attributeFilter: ['data-theme']})
    setMode(current())
    return () => observer.disconnect()
  }, [])
  return mode
}
