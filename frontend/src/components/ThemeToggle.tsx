import {useEffect, useState} from 'react'

type Theme = 'light' | 'dark'

function selectedTheme(): Theme {
  try {
    const saved = window.localStorage.getItem('kubephos-theme')
    if (saved === 'dark' || saved === 'light') return saved
  } catch {}
  if (document.documentElement.dataset.theme === 'dark') return 'dark'
  if (document.documentElement.dataset.theme === 'light') return 'light'
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(selectedTheme)
  useEffect(() => {
    document.documentElement.dataset.theme = theme
    document.documentElement.style.colorScheme = theme
    try {
      window.localStorage.setItem('kubephos-theme', theme)
    } catch {}
    const themeColor = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')
    if (themeColor) themeColor.content = theme === 'dark' ? '#0a0a0a' : '#ededed'
  }, [theme])
  const dark = theme === 'dark'
  return <button type="button" className="theme-toggle" aria-label={dark ? 'Use light mode' : 'Use dark mode'} title={dark ? 'Light mode' : 'Dark mode'} aria-pressed={dark} onClick={() => setTheme(value => value === 'dark' ? 'light' : 'dark')}>{dark
    ? <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="4"/><path d="M12 2v2m0 16v2M4.93 4.93l1.42 1.42m11.3 11.3 1.42 1.42M2 12h2m16 0h2M4.93 19.07l1.42-1.42m11.3-11.3 1.42-1.42"/></svg>
    : <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M20.5 14.2A8.4 8.4 0 0 1 9.8 3.5 8.5 8.5 0 1 0 20.5 14.2z"/></svg>}</button>
}
