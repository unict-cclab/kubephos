import {useLayoutEffect, useRef, type TableHTMLAttributes} from 'react'

/** Keep one semantic table; expose its headers beside values when rows stack. */
export function ResponsiveTable({children, className = '', ...props}: TableHTMLAttributes<HTMLTableElement>) {
  const table = useRef<HTMLTableElement>(null)
  useLayoutEffect(() => {
    const element = table.current
    if (!element) return
    const headers = [...(element.tHead?.rows[0]?.cells ?? [])].map(cell => cell.textContent?.trim() ?? '')
    for (const head of element.tHead?.querySelectorAll('th') ?? []) head.scope = 'col'
    for (const body of element.tBodies) for (const row of body.rows) {
      let index = 0
      for (const cell of row.cells) {
        if (cell.tagName === 'TD' && cell.colSpan === 1) cell.dataset.label = headers[index] ?? ''
        index += cell.colSpan
      }
    }
  }, [children])
  return <table {...props} ref={table} className={`responsive-data-table ${className}`}>{children}</table>
}
