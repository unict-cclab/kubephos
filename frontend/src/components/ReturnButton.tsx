import {ActionIcon} from './ActionIcon'

export function ReturnButton({destination, onClick}: {destination?: string; onClick: () => void}) {
  return <button type="button" className="return-button" aria-label={destination ? `Back to ${destination}` : 'Back'} onClick={onClick}><ActionIcon name="back" /><span>{destination ?? 'Back'}</span></button>
}
