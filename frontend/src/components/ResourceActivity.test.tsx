import {act, fireEvent, render, screen} from '@testing-library/react'
import {afterEach, describe, expect, it, vi} from 'vitest'
import {request} from '../api'
import type {ManagedResource, Operation, PipelineRun} from '../types'
import {ResourceActivity} from './ResourceActivity'

vi.mock('../api',()=>({request:vi.fn()}))
afterEach(()=>{vi.useRealTimers();vi.resetAllMocks()})
const resource={id:'infra',name:'ubuntu',status:'ready',operationId:'op_1'} as ManagedResource
const entry=(sequence:number,message:string)=>({sequence,message,createdAt:'2026-10-28T10:00:00Z',level:'info'})
describe('resource activity',()=>{
 it('shows template operation logs and opens the existing operation detail',async()=>{
  const operation={id:'op_1',status:'succeeded',plan:{steps:[{id:'create',name:'Create VM'}]},steps:[{id:'op_1_create',status:'succeeded'}]} as Operation
  vi.mocked(request).mockImplementation(async path=>path.includes('/logs')?{items:[entry(7,'Template ready')]}:operation)
  const open=vi.fn()
  render(<ResourceActivity resource={resource} openOperation={open}/>)
  expect(await screen.findByText('Template ready')).toBeInTheDocument()
  expect(screen.getByText('1/1')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button',{name:'Open logs'}));expect(open).toHaveBeenCalledWith('op_1')
 })
 it('polls pipeline logs incrementally and stops after completion',async()=>{
  vi.useFakeTimers()
  let reads=0
  vi.mocked(request).mockImplementation(async path=>{
   if(path.includes('/logs'))return {items:[{...entry(reads,reads===1?'Provisioning VM':'Harbor ready'),stagePosition:1,stageTitle:'Install Harbor',phase:'execution'}]}
   reads++
   return {id:'run_1',status:reads===1?'running':'succeeded',stages:[{id:'stage',title:'Install Harbor',status:reads===1?'running':'succeeded',operationId:'op_harbor'}]} as PipelineRun
  })
  const open=vi.fn()
  render(<ResourceActivity resource={{...resource,name:'harbor',status:'provisioning',pipelineRunId:'run_1'}} openOperation={()=>{}} openPipelineRun={open}/>)
  await act(async()=>{await vi.advanceTimersByTimeAsync(0)})
  expect(screen.getByText('Provisioning VM')).toBeInTheDocument()
  await act(async()=>{await vi.advanceTimersByTimeAsync(2000)})
  expect(screen.getByText('Harbor ready')).toBeInTheDocument()
  expect(vi.mocked(request)).toHaveBeenCalledWith('/pipeline-runs/run_1/logs?after=1',{},undefined,expect.any(AbortSignal))
  fireEvent.click(screen.getByRole('button',{name:'Open logs'}));expect(open).toHaveBeenCalledWith('run_1')
  await act(async()=>{await vi.advanceTimersByTimeAsync(6000)})
  expect(reads).toBe(2)
 })
 it('shows an actionable error when logs cannot be loaded',async()=>{
  vi.mocked(request).mockRejectedValue(new Error('Log service unavailable'))
  render(<ResourceActivity resource={resource} openOperation={()=>{}}/>)
  expect(await screen.findByRole('alert')).toHaveTextContent('Log service unavailable')
  expect(screen.getByRole('button',{name:'Retry'})).toBeInTheDocument()
 })
})
