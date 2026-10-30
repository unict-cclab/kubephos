import {formatDate} from '../lib'
import type {ResourceAttributionFields} from '../types'

type AttributedResource = ResourceAttributionFields & {ownerUsername?: string; ownerUserId?: string; origin?: string}

export function createdByLabel(resource: AttributedResource): string {
  return resource.createdBy || resource.ownerUsername || resource.ownerUserId || (resource.origin === 'built-in' ? 'System' : '—')
}

export function modifiedByLabel(resource: AttributedResource): string {
  if (resource.modifiedBy) return resource.modifiedBy
  if (resource.createdAt && resource.updatedAt && Date.parse(resource.createdAt) === Date.parse(resource.updatedAt)) return createdByLabel(resource)
  return '—'
}

export function ResourceAttribution({resource, editable = false}: {resource: AttributedResource; editable?: boolean}) {
  return <dl className="resource-attribution">
    <div><dt>Created</dt><dd>{resource.createdAt ? <time dateTime={resource.createdAt}>{formatDate(resource.createdAt)}</time> : '—'}</dd></div>
    <div><dt>Created by</dt><dd>{createdByLabel(resource)}</dd></div>
    {editable && <><div><dt>Last updated</dt><dd>{resource.updatedAt ? <time dateTime={resource.updatedAt}>{formatDate(resource.updatedAt)}</time> : '—'}</dd></div><div><dt>Last updated by</dt><dd>{modifiedByLabel(resource)}</dd></div></>}
  </dl>
}
