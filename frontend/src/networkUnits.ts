// Network injection keeps its existing API/storage contract in bytes per second.
export const bytesPerSecondPerMbps = 125_000
export const bandwidthMinMbps = 1 / bytesPerSecondPerMbps
export const bandwidthMaxMbps = 1_000_000_000_000 / bytesPerSecondPerMbps
export const bandwidthToMbps = (bytesPerSecond: number) => bytesPerSecond / bytesPerSecondPerMbps
export const bandwidthToBytesPerSecond = (mbps: number) => Math.round(mbps * bytesPerSecondPerMbps)
export function networkMetricValue(key: string, value: unknown): unknown {
  return key === 'bandwidthBytesPerSecond' && typeof value === 'number' ? bandwidthToMbps(value) : value
}
