import {expect,it} from 'vitest'
import {bandwidthToMbps,bandwidthToBytesPerSecond} from './networkUnits'
it.each([1,1250,9000,12500000,1000000000000])('round trips saved bandwidth %i B/s without changing its limit',value=>{
 expect(bandwidthToBytesPerSecond(bandwidthToMbps(value))).toBe(value)
})
it.each([[100,12500000],[100.5,12562500],[0.1,12500],[0.000008,1]])('converts %s Mbps to %s B/s', (mbps,bytes)=>{
 expect(bandwidthToBytesPerSecond(mbps)).toBe(bytes)
})
