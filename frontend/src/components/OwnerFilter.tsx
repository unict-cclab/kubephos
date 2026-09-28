interface OwnedResource {
  ownerUserId?: string
  ownerUsername?: string
}

interface Props {
  items: OwnedResource[]
  value: string
  onChange: (value: string) => void
}

export function ownerLabel(item: OwnedResource): string {
  return item.ownerUsername || item.ownerUserId || 'Unknown user'
}

export function OwnerFilter({items, value, onChange}: Props) {
  const owners = [...new Map(items.filter(item => item.ownerUserId).map(item => [item.ownerUserId!, ownerLabel(item)])).entries()].sort((left, right) => left[1].localeCompare(right[1]))
  return <label className="owner-filter"><span>Created by</span><select aria-label="Filter by user" value={value} onChange={event => onChange(event.target.value)}>
    <option value="">All users</option>
    {owners.map(([id, name]) => <option key={id} value={id}>{name}</option>)}
  </select></label>
}
