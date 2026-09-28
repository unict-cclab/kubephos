(function () {
  try {
    var saved = window.localStorage.getItem('kubephos-theme')
    var theme = saved === 'dark' || saved === 'light'
      ? saved
      : window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
    document.documentElement.dataset.theme = theme
    document.documentElement.style.colorScheme = theme
    var themeColor = document.querySelector('meta[name="theme-color"]')
    if (themeColor) themeColor.content = theme === 'dark' ? '#11161d' : '#f6f7f9'
  } catch (_) {}
})()
