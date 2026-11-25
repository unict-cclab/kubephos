import type {ReactNode} from 'react'

export const svgProps = {width: 16, height: 16, viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', strokeWidth: 1.75, strokeLinecap: 'round' as const, strokeLinejoin: 'round' as const, 'aria-hidden': true, focusable: 'false' as const}

export const UndoIcon = ({redo = false}: {redo?: boolean}) => <svg {...svgProps} style={redo ? {transform: 'scaleX(-1)'} : undefined}><path d="M9 14 4 9l5-5" /><path d="M4 9h10.5a5.5 5.5 0 0 1 0 11H11" /></svg>

export const EyeIcon = ({off = false}: {off?: boolean}) => <svg {...svgProps}>{off ? <><path d="M3 3l18 18" /><path d="M10.6 6.1A9.8 9.8 0 0 1 12 6c5 0 9 6 9 6a16 16 0 0 1-2.6 3.1M6.5 7.5C4.3 9.1 3 12 3 12s4 6 9 6a8.6 8.6 0 0 0 4-1" /></> : <><path d="M3 12s4-6 9-6 9 6 9 6-4 6-9 6-9-6-9-6Z" /><circle cx="12" cy="12" r="2.5" /></>}</svg>

export const layerIcons: Record<string, ReactNode> = {
  figure: <svg {...svgProps}><rect x="4" y="4" width="16" height="16" rx="2" /><path d="M4 12h16M12 4v16" /></svg>,
  title: <svg {...svgProps}><path d="M6 6h12M12 6v12" /></svg>,
  axis: <svg {...svgProps}><path d="M5 4v15h15" /><path d="M5 9h2M5 14h2M10 19v-2M15 19v-2" /></svg>,
  legend: <svg {...svgProps}><rect x="4" y="5" width="16" height="14" rx="2" /><path d="M7 10h3M13 10h4M7 14h3M13 14h4" /></svg>,
  annotation: <svg {...svgProps}><path d="M4 12h16" strokeDasharray="3 2.5" /><path d="M8 7h8" /></svg>,
  plus: <svg {...svgProps}><path d="M12 5v14M5 12h14" /></svg>,
}
