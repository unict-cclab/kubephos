import {describe, expect, it} from 'vitest'
import {fireEvent, render, screen, within} from '@testing-library/react'
import {loadGeneratorAdvice, LoadGeneratorSettings} from './LoadGeneratorAdvice'

const constant = (rps: number, seconds = 600) => [{seconds: 0, rps}, {seconds, rps}]
const settings = {replicas: 1, maxUsers: 1000, spawnRate: 10, warmupSeconds: 0}

describe('load generator sizing', () => {
  it('recommends workers, users, spawn rate and warmup for 350 RPS', () => {
    const advice = loadGeneratorAdvice(constant(350), settings)!
    expect(advice.peakRps).toBe(350)
    expect(advice.recommended.replicas).toBe(3)
    // Per-worker settings are sized for the 3 recommended workers (~117 RPS each).
    expect(advice.recommended.maxUsers).toBe(1000)
    expect(advice.recommended.spawnRate).toBe(12)
    expect(advice.recommended.warmupSeconds).toBe(60)
    expect(advice.issues.map(issue => issue.field)).toEqual(['replicas', 'spawnRate', 'warmupSeconds'])
    expect(advice.issues[0].message).toContain('needs about 3 load workers')
  })
  it('accepts a sized generator and warns when the user cap limits the tolerated latency', () => {
    expect(loadGeneratorAdvice(constant(350), {replicas: 3, maxUsers: 1000, spawnRate: 20, warmupSeconds: 120})!.issues).toEqual([])
    const capped = loadGeneratorAdvice(constant(350), {replicas: 3, maxUsers: 100, spawnRate: 20, warmupSeconds: 120})!
    expect(capped.issues.map(issue => issue.field)).toEqual(['maxUsers'])
    expect(capped.issues[0].message).toContain('below 0.6 s')
  })
  it('sizes the spawn rate for the steepest ramp and ignores empty profiles', () => {
    const ramp = [{seconds: 0, rps: 0}, {seconds: 10, rps: 400}, {seconds: 600, rps: 400}]
    expect(loadGeneratorAdvice(ramp, {...settings, replicas: 4})!.recommended.spawnRate).toBe(15)
    expect(loadGeneratorAdvice(constant(0), settings)).toBeUndefined()
  })
  it('reads the form, and applies the recommended values to it', () => {
    render(<LoadGeneratorSettings prefix="load" samples={constant(350)} values={{replicas: 1}}>
      <input aria-label="Load workers" type="number" name="load.replicas" defaultValue="1" />
      <input aria-label="Maximum users" type="number" name="load.maxUsers" defaultValue="1000" />
      <input aria-label="Spawn rate" type="number" name="load.spawnRate" defaultValue="10" />
      <input aria-label="Warmup" type="number" name="load.warmupSeconds" defaultValue="0" />
    </LoadGeneratorSettings>)
    const panel = screen.getByRole('region', {name: 'Generator sizing'})
    expect(panel).toHaveTextContent('Peak 350 RPS · 350 RPS per worker')
    fireEvent.click(within(panel).getByRole('button', {name: 'Apply recommended values'}))
    expect(screen.getByLabelText('Load workers')).toHaveValue(3)
    expect(screen.getByLabelText('Spawn rate')).toHaveValue(12)
    expect(screen.getByLabelText('Warmup')).toHaveValue(60)
    expect(screen.getByLabelText('Maximum users')).toHaveValue(1000)
    expect(within(panel).getByRole('status')).toHaveTextContent('can sustain this profile')
  })
})
