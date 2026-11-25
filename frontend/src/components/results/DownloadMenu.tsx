import {useState} from 'react'
import type {ChartExportFormat} from '../../charts'
import {ActionIcon} from '../ActionIcon'
import {ErrorFeedback, useErrorState} from '../ToastRegion'
import {HeaderMenu} from './HeaderMenu'

export const downloadFormats: Array<{format: ChartExportFormat; label: string; hint: string}> = [
  {format: 'pdf', label: 'PDF', hint: 'Vector, for papers'},
  {format: 'png', label: 'PNG', hint: '600 dpi image'},
  {format: 'svg', label: 'SVG', hint: 'Vector, editable'}
]

// "Download" button with the figure formats; `download` does the export.
export function DownloadMenu({download, label = 'Download'}: {download: (format: ChartExportFormat) => Promise<void>; label?: string}) {
  const [busy, setBusy] = useState<ChartExportFormat>()
  const [error, setError] = useErrorState()
  const run = async (format: ChartExportFormat, close: () => void) => {
    setBusy(format)
    setError('')
    try {await download(format); close()} catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not download the figure. Try again.')} finally {setBusy(undefined)}
  }
  return <>
    <HeaderMenu label={label} icon={<ActionIcon name="download" />}>{close => <div className="menu-list" role="group" aria-label="Download format">
      {downloadFormats.map(item => <button key={item.format} type="button" className="menu-item" disabled={Boolean(busy)} onClick={() => void run(item.format, close)}><span><strong>{busy === item.format ? 'Preparing…' : item.label}</strong><small>{item.hint}</small></span></button>)}
    </div>}</HeaderMenu>
    <ErrorFeedback message={error} />
  </>
}
