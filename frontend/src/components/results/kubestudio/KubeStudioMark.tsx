// KubeStudio mark: the KubePhos "K" tile whose upper arm is a rising data line ending in a
// data point. The tile wears currentColor (so it follows the surrounding text/theme) and the
// glyph is cut out in --ks-ink (defaults to the page background), legible from 14 to 32 px.

export function KubeStudioMark({size = 20, className = '', title}: {size?: number; className?: string; title?: string}) {
  return <svg className={`kubestudio-mark ${className}`.trim()} width={size} height={size} viewBox="0 0 24 24" role={title ? 'img' : undefined} aria-label={title} aria-hidden={title ? undefined : true} focusable="false">
    <rect x="1" y="1" width="22" height="22" rx="6" fill="currentColor" />
    <g fill="none" strokeLinecap="round" strokeLinejoin="round" style={{stroke: 'var(--ks-ink, var(--background, #ffffff))'}}>
      <path d="M7.5 6.5v11" strokeWidth="2.2" />
      <path d="M7.5 14.2 11 11.2l2.3 1.4 4-5.6" strokeWidth="1.9" />
      <path d="M10.6 11.6 16.4 17.5" strokeWidth="2.2" />
    </g>
    <circle cx="17.4" cy="6.9" r="1.55" style={{fill: 'var(--ks-ink, var(--background, #ffffff))'}} />
  </svg>
}
