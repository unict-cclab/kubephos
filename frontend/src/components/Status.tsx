export function statusLabel(value: string): string {
  return ({
    'built-in': 'Included', imported: 'Imported', reachable: 'Reachable', unreachable: 'Not reachable', 'access-denied': 'Access denied', checking: 'Checking', unknown: 'Unknown', pending: 'Preparing', provisioning: 'Creating',
    prechecking: 'Checking', verifying: 'Checking', validating: 'Checking', queued: 'Queued', ready: 'Ready', running: 'Running', failed: 'Failed', deleting: 'Deleting', recreating: 'Recreating', maintaining: 'Maintaining', 'maintenance-failed': 'Maintenance failed', validated: 'Validated', invalid: 'Needs review', unavailable: 'Unavailable', available: 'Available', waiting: 'Waiting',
    succeeded: 'Completed', canceled: 'Stopped', cancelled: 'Stopped',
    'recreation-failed': 'Recreation failed', 'deletion-failed': 'Deletion failed'
  } as Record<string, string>)[value.toLowerCase()] ?? value.replaceAll('-', ' ')
}

export function Status({value, title}: {value: string; title?: string}) {
  return <span className={`status ${value.toLowerCase()}`} title={title}>{statusLabel(value)}</span>
}

