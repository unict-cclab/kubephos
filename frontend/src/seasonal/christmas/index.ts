import './christmas.css'

// Christmas easter egg: from 8 December to 5 January the logo wears a Santa hat and a
// string of lights hangs over the navigation; on 25 December Santa also greets from the
// overview. Everything lives in this folder; to remove it, delete the folder and its
// import in main.tsx. To see it on any other day add ?season=christmas to the address,
// or ?season=christmas-day for Santa too.

export function isChristmasSeason(date: Date): boolean {
  const month = date.getMonth()
  const day = date.getDate()
  return month === 11 && day >= 8 || month === 0 && day <= 5
}

export const isChristmasDay = (date: Date) => date.getMonth() === 11 && date.getDate() === 25

export function applyChristmasSeason(now = new Date(), search = window.location.search) {
  const forced = new URLSearchParams(search).get('season')
  const root = document.documentElement.dataset
  if (forced === 'christmas' || forced === 'christmas-day' || isChristmasSeason(now)) root.season = 'christmas'
  if (forced === 'christmas-day' || isChristmasDay(now)) root.seasonDay = 'christmas-day'
}

applyChristmasSeason()
