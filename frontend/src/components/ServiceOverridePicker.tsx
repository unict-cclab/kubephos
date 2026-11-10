import {useState} from 'react'
import {Dialog} from './Dialog'

// Long service lists (train-ticket has dozens) get a filter field.
const filterThreshold = 8

// Opens a list of services to override; services that already have an override
// stay visible but cannot be chosen again.
export function ServiceOverridePicker({services, taken, onAdd, label = 'Add service override'}: {services: {id: string}[]; taken: (id: string) => boolean; onAdd: (id: string) => void; label?: string}) {
  const [open, setOpen] = useState(false)
  const [filter, setFilter] = useState('')
  const available = services.some(service => !taken(service.id))
  const query = filter.trim().toLowerCase()
  const visible = services.filter(service => service.id.toLowerCase().includes(query))
  const close = () => {
    setOpen(false)
    setFilter('')
  }
  const choose = (id: string) => {
    onAdd(id)
    close()
  }
  return <>
    <button type="button" className="button secondary compact" disabled={!available} onClick={() => setOpen(true)}>{label}</button>
    {open && <Dialog open title="Choose a service" onClose={close} className="service-override-picker" footer={<button type="button" className="button secondary" onClick={close}>Cancel</button>}>
      {services.length > filterThreshold && <label>Filter services<input type="search" value={filter} autoFocus onChange={event => setFilter(event.target.value)} /></label>}
      {visible.length
        ? <ul className="service-override-options" aria-label="Services">{visible.map(service => {
            const used = taken(service.id)
            return <li key={service.id}><button type="button" className="service-override-option" disabled={used} onClick={() => choose(service.id)}>
              <span>{service.id}</span>{used && <span className="service-override-used">Already overridden</span>}
            </button></li>
          })}</ul>
        : <p className="details-copy">No service matches “{filter}”.</p>}
    </Dialog>}
  </>
}
