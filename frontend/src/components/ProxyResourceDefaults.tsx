import {useEffect,useState} from 'react'
import {request} from '../api'
import type {Session} from '../types'
import {ReadoutTable} from './ReadoutTable'

export function ProxyResourceDefaults({clusterId,session,disabled=false}:{clusterId?:string;session?:Session;disabled?:boolean}) {
 const [result,setResult]=useState<{available:boolean;resources?:Record<string,string>}>()
 const [error,setError]=useState('')
 useEffect(()=>{setResult(undefined);setError('');if(!clusterId||!session||disabled)return;const controller=new AbortController();request<{available:boolean;resources?:Record<string,string>}>(`/kubernetes-clusters/${clusterId}/istio-proxy-defaults`,{},undefined,controller.signal).then(setResult).catch(()=>{if(!controller.signal.aborted)setError('Could not load Istio defaults.')});return()=>controller.abort()},[clusterId,session?.csrfToken,disabled])
 if(disabled)return <p className="details-copy">Istio is disabled on this cluster.</p>
 if(!clusterId)return <p className="details-copy">Select a cluster.</p>
 if(error)return <p className="details-copy" role="status">{error}</p>
 if(!result)return <p className="details-copy" role="status">Loading defaults…</p>
 if(!result.available)return <p className="details-copy">Istio defaults are unavailable.</p>
 return <ReadoutTable label="Default proxy resources" data={{headers:['Resource','Request','Limit'],rows:['CPU','Memory'].map(name=>[name,result.resources?.[name.toLowerCase()+'Request']??'None',result.resources?.[name.toLowerCase()+'Limit']??'None'])}}/>
}
