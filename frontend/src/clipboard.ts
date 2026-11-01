export async function copyText(value: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    try {await navigator.clipboard.writeText(value); return} catch { /* Try the user-initiated fallback, including on HTTP instances. */ }
  }
  const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
  const input = document.createElement('textarea')
  input.value = value
  input.readOnly = true
  input.style.position = 'fixed'
  input.style.opacity = '0'
  const dialogs = document.querySelectorAll('dialog[open]')
  const host = dialogs[dialogs.length - 1] ?? document.body
  host.appendChild(input)
  try {
    input.select()
    if (!document.execCommand?.('copy')) throw new Error('Copy unavailable')
  } finally {
    input.remove()
    previous?.focus({preventScroll: true})
  }
}
