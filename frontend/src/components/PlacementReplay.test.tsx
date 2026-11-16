import {act, fireEvent, render, screen, within, cleanup} from '@testing-library/react'
import {afterEach, expect, it, vi} from 'vitest'
import {PlacementReplay, usablePlacement} from './PlacementReplay'
import type {LoadedDataset} from '../resultsData'
import type {PlacementHistory} from '../types'
const history: PlacementHistory = {status:'available',source:'kube-state-metrics',stepSeconds:15,nodes:[{name:'node-a',cluster:'west',zone:'cloud'},{name:'node-b',cluster:'west',zone:'edge'}],snapshots:[{timestamp:'2026-11-15T10:00:00Z',observed:true,pods:[{uid:'a',name:'api-a',service:'api',node:'node-a',cluster:'west'},{uid:'b',name:'db-b',service:'db',node:'node-b',cluster:'west'}]},{timestamp:'2026-11-15T10:00:15Z',observed:false,pods:[]},{timestamp:'2026-11-15T10:00:30Z',observed:true,pods:[{uid:'c',name:'api-c',service:'api',node:'node-b',cluster:'west'}]}]}
const dataset=(id='run-a',placement: unknown=history)=>({artifact:{id},label:id,dataset:{spec:{placement}}}) as LoadedDataset
afterEach(()=>{cleanup();vi.useRealTimers()})
it('replays replacement placement and filters services without combining runs',()=>{
 render(<PlacementReplay datasets={[dataset()]}/>);
 const nodeA=screen.getByRole('heading',{name:'node-a'}).closest('article')!;
 expect(within(nodeA).getByText('api')).toBeInTheDocument()
 fireEvent.change(screen.getByLabelText('Service'),{target:{value:'api'}})
 expect(within(screen.getByRole('heading',{name:'node-b'}).closest('article')!).queryByText('db')).not.toBeInTheDocument()
 fireEvent.change(screen.getByRole('slider',{name:'Placement time'}),{target:{value:'2'}})
 const nodeB=screen.getByRole('heading',{name:'node-b'}).closest('article')!;
 expect(within(nodeB).getByText('api')).toBeInTheDocument()
 expect(within(nodeA).queryByText('api')).not.toBeInTheDocument()
 expect(screen.getByText('0:30')).toBeInTheDocument()
})
it('shows missing observations instead of carrying a previous placement forward',()=>{render(<PlacementReplay datasets={[dataset()]}/>);fireEvent.change(screen.getByRole('slider'),{target:{value:'1'}});expect(screen.getByText('No placement samples at this time.')).toBeInTheDocument();expect(screen.getByRole('heading',{name:'node-a'})).toBeInTheDocument();expect(screen.getAllByText('No samples')).toHaveLength(2)})
it('resets playback and filters when changing run; old artifacts stay supported',()=>{render(<PlacementReplay datasets={[dataset(),dataset('legacy',null)]}/>);fireEvent.change(screen.getByLabelText('Service'),{target:{value:'api'}});fireEvent.change(screen.getByLabelText('Placement run'),{target:{value:'legacy'}});expect(screen.getByText('Placement history is not available for this run.')).toBeInTheDocument();expect(screen.queryByRole('slider')).not.toBeInTheDocument()})
it('plays at the selected speed and stops at the end',()=>{vi.useFakeTimers();render(<PlacementReplay datasets={[dataset()]}/>);fireEvent.change(screen.getByLabelText('Playback speed'),{target:{value:'15'}});fireEvent.click(screen.getByRole('button',{name:'Play'}));act(()=>vi.advanceTimersByTime(1000));expect(screen.getByRole('slider')).toHaveValue('1');act(()=>vi.advanceTimersByTime(1000));expect(screen.getByRole('slider')).toHaveValue('2');expect(screen.getByRole('button',{name:'Play'})).toBeInTheDocument()})
it('rejects malformed and non-monotonic evidence',()=>{expect(usablePlacement({...history,snapshots:[history.snapshots[2],history.snapshots[0]]})).toBe(false);expect(usablePlacement({...history,nodes:[{name:null}]})).toBe(false)})
