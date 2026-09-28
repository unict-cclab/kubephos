export function KubernetesLogo({className = ''}: {className?: string}) {
  return <span className={`kubernetes-logo ${className}`.trim()} aria-hidden="true">
    <svg viewBox="0 0 48 48" role="presentation">
      <path d="M24 2.8 42.4 13.4v21.2L24 45.2 5.6 34.6V13.4L24 2.8Z" fill="#326ce5" />
      <g fill="none" stroke="#fff" strokeLinecap="round" strokeWidth="2.5">
        {Array.from({length: 7}, (_, index) => <g key={index} transform={`rotate(${index * 360 / 7} 24 24)`}><path d="M24 9.2v10.1" /><circle cx="24" cy="8.2" r="1.7" fill="#fff" stroke="none" /></g>)}
        <circle cx="24" cy="24" r="6.2" fill="#326ce5" />
      </g>
    </svg>
  </span>
}
