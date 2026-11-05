import {useState} from 'react'
import type {Application} from '../types'
import {ActionIcon} from './ActionIcon'
import {ResourceAction} from './ResourceAction'

export function applicationServices(application: Application): string {
  if (application.descriptor.spec?.valuesSchema?.['x-kubephos-dynamic-topology']) return 'Configurable services'
  const count = application.descriptor.spec?.interface?.components?.length ?? 0
  return count === 1 ? '1 service' : `${count} services`
}

// First and last word, so "Bank of Anthos" reads BA rather than BO.
export function applicationMonogram(name: string): string {
  const words = name.split(/\s+/).filter(Boolean)
  return (words.length > 1 ? [words[0], words[words.length - 1]] : words).map(word => word.charAt(0).toUpperCase()).join('')
}

// Each card opens the application through its title button, which stretches over the whole card.
export function ApplicationGrid({items, isAdmin, open, remove}: {items: Application[]; isAdmin: boolean; open: (application: Application) => void; remove: (application: Application) => Promise<boolean>}) {
  const [query, setQuery] = useState('')
  const search = query.trim().toLowerCase()
  const visible = search ? items.filter(item => `${item.name} ${item.description}`.toLowerCase().includes(search)) : items
  return <section className="application-catalog" aria-label="Applications">
    <div className="list-toolbar">
      <input type="search" aria-label="Search applications" placeholder="Search applications" value={query} onChange={event => setQuery(event.target.value)} />
      <span>{visible.length} of {items.length}</span>
    </div>
    {visible.length
      ? <ul className="application-grid">{visible.map(item => <li key={item.reference} className="application-card">
          <div className="application-card-head">
            <span className="application-monogram" aria-hidden="true">{applicationMonogram(item.name)}</span>
            <span className="application-origin">{item.origin === 'built-in' ? 'Included' : 'Imported'}</span>
          </div>
          <h3><button type="button" className="application-card-open" onClick={() => open(item)}>{item.name}</button></h3>
          {item.description && <p>{item.description}</p>}
          <div className="application-card-foot">
            <span>{applicationServices(item)}</span>
            {isAdmin && <ResourceAction action="delete" label={`Delete ${item.name}`} onClick={() => void remove(item)} />}
            <span className="application-card-arrow" aria-hidden="true"><ActionIcon name="forward" /></span>
          </div>
        </li>)}</ul>
      : <div className="table-empty"><div className="table-empty-copy">
          <strong>{items.length ? 'No matching applications' : 'No applications yet'}</strong>
          <p>{items.length ? 'Try a different name or description.' : 'Import an application descriptor to add it to the catalog.'}</p>
        </div></div>}
  </section>
}
