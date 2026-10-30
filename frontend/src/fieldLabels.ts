const standardLabels: Record<string, string> = {
  name: 'Name',
  image: 'Image',
  sourceImage: 'Image',
  configFile: 'Configuration',
  intervalSeconds: 'Interval (s)',
  periodSeconds: 'Period (s)',
  cores: 'CPU (cores)',
  memoryMiB: 'Memory (MiB)',
  diskGiB: 'Disk (GiB)',
  applicationPools: 'Pools',
  capacity: 'Resources',
  machineCapacity: 'Resources',
  controlPlaneCapacity: 'Resources'
}

const standardTitles: Record<string, string> = {
  'Mirrored strategy image': 'Image',
  'Scheduler configuration': 'Configuration',
  'Descheduler policy': 'Configuration',
  'Autoscaler parameters': 'Configuration'
}

export function standardFieldLabel(name: string, title?: string): string | undefined {
  return standardLabels[name] ?? (title ? standardTitles[title] : undefined)
}
