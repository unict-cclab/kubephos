import {dashArray, type LineStyle, type MarkerShape} from './figureSpec'
import type {FigureModel, ModelAnnotation, ModelAxis, ModelPanel, ModelSeries} from './figureModel'
import type {FigureSettings} from './figureSettings'

// Reproducibility exports: a self-contained matplotlib script or a pgfplots snippet, each
// with a CSV of exactly the plotted values (display units). Both are generated from the
// same FigureModel as the SVG/PDF, with the same size, fonts, colours, markers, axes and
// annotations.

export interface CodeOptions {
  base: string
  title: string
  caption: string
  settings: FigureSettings
  // Figure size in points (the rendered SVG viewBox).
  width: number
  height: number
  grid: 'none' | 'major' | 'minor'
}

interface Column {name: string; values: number[]}
interface SeriesColumns {series: ModelSeries; panel: ModelPanel; id: string; x: string; y: string; lo?: string; hi?: string; runs: string[]; bands: Array<[string, string]>; fx?: string; fy?: string}

const numberText = (value: number | undefined) => value === undefined || !Number.isFinite(value) ? 'nan' : String(Number(value.toPrecision(10)))

// Wide CSV: one column group per series (x, y, band, repetitions, fit), padded with nan.
export function plotData(model: FigureModel): {csv: string; columns: SeriesColumns[]} {
  const columns: Column[] = []
  const add = (name: string, values: number[]) => {columns.push({name, values}); return name}
  const described = model.panels.flatMap(panel => panel.series.map((series, index): SeriesColumns => {
    const id = `p${panel.index + 1}s${index + 1}`
    const entry: SeriesColumns = {series, panel, id, x: add(`${id}_x`, series.x), y: add(`${id}_y`, series.y), runs: [], bands: []}
    if (series.low && series.high) {
      entry.lo = add(`${id}_lo`, series.low)
      entry.hi = add(`${id}_hi`, series.high)
      // Closed outlines per contiguous segment (pgfplots fills them directly).
      let run: number[] = []
      const flush = () => {
        if (run.length > 1 && entry.bands.length < 20) {
          const k = entry.bands.length + 1
          const xs = [...run.map(i => series.x[i]), ...[...run].reverse().map(i => series.x[i])]
          const ys = [...run.map(i => series.high![i]), ...[...run].reverse().map(i => series.low![i])]
          entry.bands.push([add(`${id}_band${k}_x`, xs), add(`${id}_band${k}_y`, ys)])
        }
        run = []
      }
      series.x.forEach((value, i) => Number.isFinite(value) && Number.isFinite(series.low![i]) && Number.isFinite(series.high![i]) ? run.push(i) : flush())
      flush()
    }
    series.runs?.forEach((item, k) => entry.runs.push(add(`${id}_run${k + 1}`, item.y)))
    if (series.fit) {entry.fx = add(`${id}_fit_x`, series.fit.x); entry.fy = add(`${id}_fit_y`, series.fit.y)}
    return entry
  }))
  const length = Math.max(0, ...columns.map(column => column.values.length))
  const rows = [columns.map(column => column.name).join(','), ...Array.from({length}, (_, row) => columns.map(column => numberText(column.values[row])).join(','))]
  return {csv: `${rows.join('\n')}\n`, columns: described}
}

const inches = (points: number) => Number((points / 72).toFixed(3))
const py = (value: string) => JSON.stringify(value)
const pyNumber = (value: number) => Number.isFinite(value) ? String(Number(value.toPrecision(10))) : 'float("nan")'
const pyList = (values: number[]) => `[${values.map(pyNumber).join(', ')}]`
const mplMarker: Record<MarkerShape, string> = {circle: 'o', square: 's', triangle: '^', diamond: 'D', cross: '+', x: 'x', none: 'None'}
const mplDash = (line: LineStyle, width: number) => {
  if (line === 'solid') return '"-"'
  if (line === 'none') return '"None"'
  // Dash lengths in points divided by the line width (matplotlib scales dashes by it).
  const pattern = (dashArray(line, width) ?? '').split(' ').map(Number).map(value => Number((value / Math.max(.1, width)).toFixed(3)))
  return `(0, (${pattern.join(', ')}))`
}
const markEvery = (series: ModelSeries) => series.kind === 'scatter' ? 1 : Math.max(1, Math.round(series.x.filter(Number.isFinite).length / 7))
const legendLoc: Record<string, string> = {bottom: 'outside lower center', top: 'outside upper center', right: 'outside right upper'}

function pyAxis(target: string, axis: ModelAxis, which: 'x' | 'y', show: boolean): string[] {
  const lines = [`${target}.set_${which}lim(${pyNumber(axis.scale.lower)}, ${pyNumber(axis.scale.upper)})`]
  if (axis.scale.log) lines.unshift(`${target}.set_${which}scale("log")`)
  lines.push(`${target}.set_${which}ticks(${pyList(axis.scale.ticks)})`)
  lines.push(show ? `${target}.set_${which}ticklabels(${`[${axis.scale.ticks.map(tick => py(axis.format(tick))).join(', ')}]`})` : `${target}.tick_params(axis="${which}", ${which === 'x' ? 'labelbottom' : 'labelleft'}=False)`)
  lines.push(axis.minor.length ? `${target}.set_${which}ticks(${pyList(axis.minor)}, minor=True)` : `${target}.${which}axis.set_minor_locator(NullLocator())`)
  if (show) lines.push(`${target}.set_${which}label(${py(axis.label)}${axis.color ? `, color=${py(axis.color)}` : ''})`)
  if (axis.color) lines.push(`${target}.tick_params(axis="${which}", colors=${py(axis.color)})`)
  return lines
}

function pyAnnotation(item: ModelAnnotation, ax: string, ax2: string, fs: number): string[] {
  const target = item.axis === 'right' ? ax2 : ax
  const label = item.label ? py(item.label) : ''
  if (item.kind === 'vline') return [`${ax}.axvline(${pyNumber(item.x!)}, color=${py(item.color)}, lw=0.5, ls=${item.source === 'user' ? '(0, (3, 2))' : '(0, (1, 1.6))'}, zorder=4)`, ...(label ? [`${ax}.annotate(${label}, xy=(${pyNumber(item.x!)}, 1), xycoords=("data", "axes fraction"), xytext=(2, -2), textcoords="offset points", va="top", fontsize=${fs * .78}, color="#555555")`.replace('xytext=(2, -2)', item.source === 'warmup' ? `xytext=(2, ${-(fs * .9 + 2).toFixed(1)})` : 'xytext=(2, -2)')] : [])]
  if (item.kind === 'hline') return [`${target}.axhline(${pyNumber(item.y!)}, color=${py(item.color)}, lw=${item.source === 'slo' ? 0.9 : 0.7}, ls=(0, (${item.source === 'slo' ? '4, 3' : '3, 2'})), zorder=4)`, ...(label ? [`${target}.annotate(${label}, xy=(1, ${pyNumber(item.y!)}), xycoords=("axes fraction", "data"), xytext=(-2, 2), textcoords="offset points", ha="right", fontsize=${fs * .9}, color=${py(item.color)})`] : [])]
  if (item.kind === 'xrange') return [`${ax}.axvspan(${pyNumber(item.x!)}, ${pyNumber(item.x2!)}, color="black", alpha=0.07, lw=0, zorder=0)`, ...(label ? [`${ax}.annotate(${label}, xy=(${pyNumber((item.x! + item.x2!) / 2)}, 1), xycoords=("data", "axes fraction"), xytext=(0, ${-(fs + 2).toFixed(1)}), textcoords="offset points", ha="center", va="top", fontsize=${fs * .85}, color="#333333")`] : [])]
  return [`${target}.text(${pyNumber(item.x!)}, ${pyNumber(item.y!)}, ${label}, fontsize=${fs * .9}, color=${py(item.color)}, zorder=5)`]
}

export function matplotlibScript(model: FigureModel, options: CodeOptions): {script: string; csv: string; csvName: string; scriptName: string} {
  const {csv, columns} = plotData(model)
  const csvName = `${options.base}-plot-data.csv`
  const fs = options.settings.fontSize
  const serif = options.settings.font === 'serif'
  const multi = model.panels.length > 1
  const sharedX = multi && model.panels.every(panel => panel.x.scale.lower === model.panels[0].x.scale.lower && panel.x.scale.upper === model.panels[0].x.scale.upper)
  const lines: string[] = [
    '#!/usr/bin/env python3',
    `"""${options.title.replaceAll('"""', "'''")}`,
    '',
    `Generated by KubePhos KubeStudio. Recreates the figure from ${csvName}`,
    `(the plotted values, in display units). Requires Python 3.9+ and matplotlib >= 3.7.`,
    '',
    `Caption: ${options.caption.replaceAll('"""', "'''").replaceAll('\\', '/')}`,
    '"""',
    'import csv',
    'import math',
    'from pathlib import Path',
    '',
    'import matplotlib',
    'matplotlib.use("Agg")',
    'import matplotlib.pyplot as plt',
    'from matplotlib.lines import Line2D',
    'from matplotlib.ticker import NullLocator',
    '',
    `DATA = Path(__file__).with_name(${py(csvName)})`,
    `OUTPUT = Path(__file__).with_name(${py(`${options.base}.pdf`)})`,
    `WIDTH_IN, HEIGHT_IN = ${inches(options.width)}, ${inches(options.height)}  # ${options.width.toFixed(1)} × ${options.height.toFixed(1)} pt`,
    '',
    'plt.rcParams.update({',
    `    "font.family": ${py(serif ? 'serif' : 'sans-serif')},`,
    `    "font.${serif ? 'serif' : 'sans-serif'}": ${serif ? '["Times New Roman", "Times", "Nimbus Roman", "Liberation Serif", "DejaVu Serif"]' : '["Helvetica", "Arial", "Liberation Sans", "Nimbus Sans", "DejaVu Sans"]'},`,
    `    "mathtext.fontset": ${py(serif ? 'stix' : 'dejavusans')},`,
    `    "font.size": ${fs},`,
    '    "axes.linewidth": 0.6, "xtick.major.width": 0.6, "ytick.major.width": 0.6,',
    `    "xtick.major.size": ${(fs * .35).toFixed(2)}, "ytick.major.size": ${(fs * .35).toFixed(2)}, "xtick.minor.size": 0, "ytick.minor.size": 0,`,
    '    "legend.frameon": False, "legend.handlelength": 2.0,',
    '    "pdf.fonttype": 42, "ps.fonttype": 42, "svg.fonttype": "none",',
    '})',
    '',
    'with DATA.open(newline="") as handle:',
    '    ROWS = list(csv.DictReader(handle))',
    '',
    '',
    'def col(name):',
    '    """One column as floats; nan marks gaps (missing windows) and padding."""',
    '    return [float(row[name]) if row[name] not in ("", "nan") else math.nan for row in ROWS]',
    '',
    '',
    `fig, axes = plt.subplots(${model.rows}, ${model.columns}, figsize=(WIDTH_IN, HEIGHT_IN), squeeze=False, layout="constrained"${sharedX ? ', sharex=True' : ''})`,
    'handles = {}'
  ]
  if (options.settings.showTitle) lines.push(`fig.suptitle(${py(options.title)}, fontsize=${fs * 1.1}, fontweight="bold")`)
  for (const panel of model.panels) {
    const row = Math.floor(panel.index / model.columns)
    const column = panel.index % model.columns
    const ax = `ax${panel.index + 1}`
    const ax2 = `${ax}r`
    lines.push('', `# Panel ${panel.index + 1}${panel.letter ? ` ${panel.letter}` : ''}`, `${ax} = axes[${row}][${column}]`)
    if (panel.right) lines.push(`${ax2} = ${ax}.twinx()`)
    for (const entry of columns.filter(item => item.panel === panel)) {
      const series = entry.series
      const style = series.style
      const target = series.axis === 'right' ? ax2 : ax
      const alpha = style.opacity < 1 ? `, alpha=${style.opacity}` : ''
      lines.push(`# ${series.label} (${series.axis} axis)`, `x = col(${py(entry.x)})`)
      if (entry.lo && entry.hi) lines.push(`${target}.fill_between(x, col(${py(entry.lo)}), col(${py(entry.hi)}), color=${py(style.color)}, alpha=${(.16 * style.opacity).toFixed(3)}, lw=0, zorder=1)`)
      if (entry.runs.length) lines.push(`for name in [${entry.runs.map(py).join(', ')}]:`, `    ${target}.plot(x, col(name), color=${py(style.color)}, lw=${(series.showMean ? style.lineWidth * .45 : style.lineWidth * .7).toFixed(2)}, alpha=${series.showMean ? .38 * style.opacity : style.opacity}, zorder=2)`)
      if (series.showMean) {
        const width = series.runs && series.showMean ? style.lineWidth * 1.35 : style.lineWidth
        const marker = mplMarker[style.marker]
        const size = (series.kind === 'scatter' ? style.markerSize * .85 : style.markerSize) * 2
        lines.push(`handles[${py(series.key)}] = ${target}.plot(x, col(${py(entry.y)}), color=${py(style.color)}, lw=${width.toFixed(2)}, ls=${mplDash(style.line, width)}, marker=${py(marker)}, ms=${size.toFixed(2)}, mfc=${style.hollow ? '"white"' : py(style.color)}, mec=${py(style.color)}, mew=${(style.markerSize * .45).toFixed(2)}, markevery=${markEvery(series)}${alpha}, label=${py(series.label)}, zorder=3)[0]`)
      }
      if (entry.fx && entry.fy) lines.push(`${target}.plot(col(${py(entry.fx)}), col(${py(entry.fy)}), color=${py(style.color)}, lw=${style.lineWidth.toFixed(2)}${series.axis === 'right' ? ', ls="--"' : ''}${alpha}, zorder=3)`)
    }
    for (const item of panel.annotations) {
      const visible = item.kind === 'hline' || item.kind === 'text' ? item.axis === 'left' || panel.right : true
      if (visible) lines.push(...pyAnnotation(item, ax, ax2, fs))
    }
    const showX = !sharedX || row === model.rows - 1
    lines.push(...pyAxis(ax, panel.x, 'x', showX))
    if (panel.left) lines.push(...pyAxis(ax, panel.left, 'y', true))
    else lines.push(`${ax}.set_yticks([])`)
    if (panel.right) lines.push(...pyAxis(ax2, panel.right, 'y', true))
    const gridAxis = panel.left ? ax : panel.right ? ax2 : ''
    if (options.grid !== 'none' && gridAxis) {
      lines.push(`${gridAxis}.grid(axis=${py(model.type === 'xy' ? 'both' : 'y')}, which="major", color="#e3e3e3", lw=0.45)`)
      if (options.grid === 'minor') lines.push(`${gridAxis}.grid(axis="y", which="minor", color="#f0f0f0", lw=0.35)`)
      lines.push(`${ax}.set_axisbelow(True)`)
    }
    if (panel.letter) lines.push(`${ax}.set_title(${py(panel.letter)}, loc="left", fontweight="bold", fontsize=${fs})`)
  }
  const position = options.settings.legend
  if (position !== 'none' && model.legend.length > 1) {
    const header = (text: string) => `Line2D([], [], ls="None", label=${py(text)})`
    // One legend row per axis group (header first); matplotlib fills columns, so interleave.
    const groups: string[][] = []
    model.legend.forEach(item => {if (item.header || !groups.length) groups.push(item.header ? [header(item.header)] : []); groups.at(-1)!.push(`handles[${py(item.series.key)}]`)})
    const flowing = position === 'top' || position === 'bottom'
    const columns = flowing ? groups.length > 1 ? Math.max(...groups.map(group => group.length)) : Math.min(groups[0].length, 4) : 1
    const items = flowing && groups.length > 1 ? Array.from({length: columns}, (_, column) => groups.map(group => group[column] ?? 'Line2D([], [], ls="None", label="")')).flat() : groups.flat()
    lines.push('', `legend_handles = [${items.join(', ')}]`)
    if (position === 'inside') lines.push(`axes[0][0].legend(handles=legend_handles, loc="upper right", frameon=True, framealpha=0.88, edgecolor="#bdbdbd", fontsize=${fs})`)
    else lines.push(`fig.legend(handles=legend_handles, loc=${py(legendLoc[position])}, ncol=${columns}, fontsize=${fs}, columnspacing=0.9, handlelength=1.6, handletextpad=0.4)`)
  }
  lines.push('', 'fig.savefig(OUTPUT)', 'fig.savefig(OUTPUT.with_suffix(".png"), dpi=600)', 'print(f"Wrote {OUTPUT}")', '')
  return {script: lines.join('\n'), csv, csvName, scriptName: `${options.base}.py`}
}

// ---------------------------------------------------------------------------------------
// pgfplots

const tex = (value: string) => value.replaceAll('\\', '\\textbackslash{}').replaceAll(/([%&_#${}])/g, '\\$1').replaceAll('~', '\\textasciitilde{}').replaceAll('^', '\\textasciicircum{}').replaceAll('−', '-').replaceAll('·', '$\\cdot$').replaceAll('×', '$\\times$').replaceAll('–', '--').replaceAll('—', '---')
const texNumber = (value: number) => Number.isFinite(value) ? String(Number(value.toPrecision(8))) : 'nan'
const colorName = (hex: string) => `kp${hex.slice(1).toLowerCase()}`
const pgfMarker = (shape: MarkerShape, hollow: boolean) => ({circle: hollow ? 'o' : '*', square: hollow ? 'square' : 'square*', triangle: hollow ? 'triangle' : 'triangle*', diamond: hollow ? 'diamond' : 'diamond*', cross: '+', x: 'x', none: ''}[shape])
const pgfDash = (line: LineStyle, width: number) => {
  if (line === 'solid') return 'solid'
  if (line === 'none') return 'draw=none'
  const pattern = (dashArray(line, width) ?? '').split(' ').map(Number)
  return `dash pattern=${pattern.map((value, index) => `${index % 2 ? 'off' : 'on'} ${value}pt`).join(' ')}`
}

function pgfStyle(series: ModelSeries, forLegend = false): string {
  const style = series.style
  const parts = [`color=${colorName(style.color)}`, `line width=${(series.runs && series.showMean ? style.lineWidth * 1.35 : style.lineWidth).toFixed(2)}pt`, pgfDash(series.kind === 'scatter' && !forLegend ? style.line : style.line, style.lineWidth)]
  const mark = pgfMarker(style.marker, style.hollow)
  if (mark) parts.push(`mark=${mark}`, `mark size=${(series.kind === 'scatter' ? style.markerSize * .85 : style.markerSize).toFixed(2)}pt`, `mark options={solid${style.hollow ? ', fill=white' : ''}}`)
  else parts.push('no marks')
  if (!forLegend && series.kind === 'line' && mark) parts.push(`mark repeat=${markEvery(series)}`)
  if (style.opacity < 1) parts.push(`opacity=${style.opacity}`)
  return parts.join(', ')
}

function pgfAxisOptions(axis: ModelAxis, which: 'x' | 'y', show: boolean): string[] {
  const options = [`${which}min=${texNumber(axis.scale.lower)}`, `${which}max=${texNumber(axis.scale.upper)}`, `${which}tick={${axis.scale.ticks.map(texNumber).join(',')}}`]
  options.push(show ? `${which}ticklabels={${axis.scale.ticks.map(tick => `{${tex(axis.format(tick))}}`).join(',')}}` : `${which}ticklabels={}`)
  if (axis.minor.length) options.push(`minor ${which}tick={${axis.minor.map(texNumber).join(',')}}`)
  if (axis.scale.log) options.push(`${which}mode=log`)
  if (show) options.push(`${which}label={${tex(axis.label)}}`)
  if (axis.color) options.push(`${which}label style={color=${colorName(axis.color)}}`, `${which}ticklabel style={color=${colorName(axis.color)}}`)
  return options
}

export function pgfplotsSnippet(model: FigureModel, options: CodeOptions): {tex: string; csv: string; csvName: string; texName: string} {
  const {csv, columns} = plotData(model)
  const csvName = `${options.base}-plot-data.csv`
  const fs = options.settings.fontSize
  const colors = [...new Set([...columns.map(item => item.series.style.color), ...model.panels.flatMap(panel => panel.annotations.map(item => item.color)), ...model.panels.flatMap(panel => [panel.left?.color, panel.right?.color].filter((value): value is string => Boolean(value)))])]
  const multi = model.panels.length > 1
  const sharedX = multi && model.panels.every(panel => panel.x.scale.lower === model.panels[0].x.scale.lower && panel.x.scale.upper === model.panels[0].x.scale.upper)
  // Plot box per panel: the cell minus room for tick labels and axis titles.
  const cellWidth = options.width / model.columns
  const cellHeight = (options.height - fs * (options.settings.legend === 'top' || options.settings.legend === 'bottom' ? 2.4 : 0.8)) / model.rows
  const boxWidth = Math.max(36, cellWidth - fs * (model.panels.some(panel => panel.right) ? 8.4 : 5.2))
  const boxHeight = Math.max(30, cellHeight - fs * 3.9)
  const table = (x: string, y: string) => `table[x=${x}, y=${y}, col sep=comma] {${csvName}}`
  const lines: string[] = [
    `% ${tex(options.title)}`,
    `% Generated by KubePhos KubeStudio. Plotted values: ${csvName} (display units).`,
    `% Figure ${inches(options.width)} x ${inches(options.height)} in; ${options.settings.font === 'serif' ? 'serif' : 'sans-serif'} ${fs} pt.`,
    '% Preamble: \\usepackage{pgfplots} \\pgfplotsset{compat=1.18}',
    ...colors.map(color => `\\definecolor{${colorName(color)}}{HTML}{${color.slice(1).toUpperCase()}}`),
    `\\begin{tikzpicture}[font=\\fontsize{${fs}}{${(fs * 1.2).toFixed(1)}}\\selectfont${options.settings.font === 'sans' ? '\\sffamily' : ''}]`,
    `\\pgfplotsset{every axis/.append style={scale only axis, width=${(boxWidth / 72).toFixed(3)}in, height=${(boxHeight / 72).toFixed(3)}in, unbounded coords=jump, tick align=outside, tick pos=left, axis line style={line width=0.6pt}, major tick length=${(fs * .35).toFixed(2)}pt, minor tick length=0pt, label style={font=\\fontsize{${fs}}{${(fs * 1.2).toFixed(1)}}\\selectfont}, ${options.grid === 'none' ? 'grid=none' : options.grid === 'minor' ? 'grid=both, minor grid style={color=black!6, line width=0.35pt}' : `grid=${model.type === 'xy' ? 'major' : 'none'}, ymajorgrids`}, major grid style={color=black!11, line width=0.45pt}, scaled ticks=false}}`
  ]
  for (const panel of model.panels) {
    const row = Math.floor(panel.index / model.columns)
    const column = panel.index % model.columns
    const at = `at={(${(column * cellWidth / 72).toFixed(3)}in, ${(-row * cellHeight / 72).toFixed(3)}in)}`
    const showX = !sharedX || row === model.rows - 1
    const base = [`name=panel${panel.index + 1}`, at, ...pgfAxisOptions(panel.x, 'x', showX), ...(panel.left ? pgfAxisOptions(panel.left, 'y', true) : ['ytick=\\empty'])]
    if (panel.letter) base.push(`title={\\textbf{${panel.letter}}}`, 'title style={at={(0,1)}, anchor=south west}')
    // Bottom legend: under the bottom-left panel so it never lands inside a lower panel.
    const legendPanel = options.settings.legend === 'bottom' ? (model.rows - 1) * model.columns : 0
    const legendHere = panel.index === legendPanel && options.settings.legend !== 'none' && model.legend.length > 1
    if (legendHere) {
      const place = options.settings.legend === 'right' ? 'at={(1.03,1)}, anchor=north west, legend columns=1' : options.settings.legend === 'top' ? 'at={(0.5,1.03)}, anchor=south, legend columns=-1' : options.settings.legend === 'inside' ? 'at={(0.98,0.98)}, anchor=north east, fill=white, fill opacity=0.88, draw=black!25, text opacity=1' : model.columns > 1 ? 'at={(0,-0.3)}, anchor=north west, legend columns=-1' : 'at={(0.5,-0.36)}, anchor=north, legend columns=-1'
      base.push(`legend style={${place}${options.settings.legend === 'inside' ? '' : ', draw=none'}, /tikz/every even column/.append style={column sep=0.6em}}`, 'legend cell align=left')
    }
    lines.push(`% Panel ${panel.index + 1}${panel.letter ? ` ${panel.letter}` : ''}`, `\\begin{axis}[${base.join(',\n  ')}]`)
    const draw = (side: 'left' | 'right') => {
      for (const entry of columns.filter(item => item.panel === panel && item.series.axis === side)) {
        const series = entry.series
        const color = colorName(series.style.color)
        lines.push(`% ${tex(series.label)}`)
        for (const [bx, by] of entry.bands) lines.push(`\\addplot[draw=none, fill=${color}, fill opacity=${(.16 * series.style.opacity).toFixed(3)}, forget plot] ${table(bx, by)} -- cycle;`)
        for (const run of entry.runs) lines.push(`\\addplot[${series.showMean ? `color=${color}!38!white, line width=${(series.style.lineWidth * .45).toFixed(2)}pt` : `color=${color}, line width=${(series.style.lineWidth * .7).toFixed(2)}pt`}, no marks, forget plot] ${table(entry.x, run)};`)
        if (series.showMean) lines.push(`\\addplot[${pgfStyle(series)}${series.kind === 'scatter' && series.style.line === 'none' ? ', only marks' : ''}, forget plot] ${table(entry.x, entry.y)};`)
        if (entry.fx && entry.fy) lines.push(`\\addplot[color=${color}, line width=${series.style.lineWidth.toFixed(2)}pt, no marks${side === 'right' ? ', dashed' : ''}, forget plot] ${table(entry.fx, entry.fy)};`)
      }
      for (const item of panel.annotations) {
        const color = colorName(item.color)
        if (item.kind === 'hline' && item.axis === side) lines.push(`\\draw[${color}, line width=${item.source === 'slo' ? 0.9 : 0.7}pt, dash pattern=on ${item.source === 'slo' ? 4 : 3}pt off ${item.source === 'slo' ? 3 : 2}pt] ({rel axis cs:0,0} |- {axis cs:0,${texNumber(item.y!)}}) -- ({rel axis cs:1,0} |- {axis cs:0,${texNumber(item.y!)}})${item.label ? ` node[pos=1, above left, inner sep=1pt, font=\\scriptsize, text=${color}] {${tex(item.label)}}` : ''};`)
        if (item.kind === 'text' && item.axis === side) lines.push(`\\node[anchor=base west, inner sep=0pt, text=${color}] at (axis cs:${texNumber(item.x!)},${texNumber(item.y!)}) {${tex(item.label ?? '')}};`)
        if (side !== 'left') continue
        if (item.kind === 'vline') lines.push(`\\draw[${color}, line width=0.5pt, dash pattern=${item.source === 'user' ? 'on 3pt off 2pt' : 'on 1pt off 1.6pt'}] ({axis cs:${texNumber(item.x!)},0} |- {rel axis cs:0,0}) -- ({axis cs:${texNumber(item.x!)},0} |- {rel axis cs:0,1})${item.label ? ` node[below right, inner sep=1pt, font=\\tiny, text=black!67${item.source === 'warmup' ? `, yshift=-${fs}pt` : ''}] {${tex(item.label)}}` : ''};`)
        if (item.kind === 'xrange') lines.push(`\\fill[black, fill opacity=0.07] ({axis cs:${texNumber(item.x!)},0} |- {rel axis cs:0,0}) rectangle ({axis cs:${texNumber(item.x2!)},0} |- {rel axis cs:0,1});${item.label ? `\n\\node[below, inner sep=1pt, font=\\scriptsize] at ({axis cs:${texNumber((item.x! + item.x2!) / 2)},0} |- {rel axis cs:0,1}) {${tex(item.label)}};` : ''}`)
      }
    }
    draw('left')
    if (legendHere) {
      model.legend.forEach(item => {
        if (item.header) lines.push(`\\addlegendimage{empty legend}\\addlegendentry{${tex(item.header)}}`)
        lines.push(`\\addlegendimage{${pgfStyle(item.series, true)}${item.series.kind === 'scatter' && item.series.style.line === 'none' ? ', only marks' : ''}}\\addlegendentry{${tex(item.series.label)}}`)
      })
    }
    lines.push('\\end{axis}')
    if (panel.right) {
      lines.push(`\\begin{axis}[${[at, 'axis y line*=right', 'axis x line=none', 'ytick pos=right', 'yticklabel pos=right', 'grid=none', `xmin=${texNumber(panel.x.scale.lower)}`, `xmax=${texNumber(panel.x.scale.upper)}`, ...(panel.x.scale.log ? ['xmode=log'] : []), ...pgfAxisOptions(panel.right, 'y', true)].join(',\n  ')}]`)
      draw('right')
      lines.push('\\end{axis}')
    }
  }
  if (options.settings.showTitle) lines.push(`\\node[anchor=south, font=\\bfseries] at (current bounding box.north) {${tex(options.title)}};`)
  lines.push('\\end{tikzpicture}', '')
  return {tex: lines.join('\n'), csv, csvName, texName: `${options.base}.tex`}
}
