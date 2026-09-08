import {StrictMode, Component, type ErrorInfo, type ReactNode} from 'react'
import {createRoot} from 'react-dom/client'
import App from './App'
import '../../internal/webui/static/styles.css'

class ErrorBoundary extends Component<{children: ReactNode}, {failed: boolean}> {
  state = {failed: false}

  static getDerivedStateFromError() {
    return {failed: true}
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error(error, info)
  }

  render() {
    if (this.state.failed) return <main className="fatal-screen"><span className="brand-mark">K</span><h1>KubePhos needs to reload</h1><p>The interface encountered an unexpected error. No background operation was interrupted.</p><button className="button primary" onClick={() => window.location.reload()}>Reload interface</button></main>
    return this.props.children
  }
}

createRoot(document.getElementById('root')!).render(<StrictMode><ErrorBoundary><App /></ErrorBoundary></StrictMode>)
