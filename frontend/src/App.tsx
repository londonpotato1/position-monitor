import { HashRouter, Routes, Route, Navigate } from 'react-router-dom'
import { Component, type ReactNode } from 'react'
import Layout from './components/Layout'
import Hedged from './pages/Hedged'
import Unmatched from './pages/Unmatched'
import Dashboard from './pages/Dashboard'
import Settings from './pages/Settings'

class ErrorBoundary extends Component<{ children: ReactNode }, { error: string | null }> {
  state = { error: null as string | null }
  static getDerivedStateFromError(err: Error) { return { error: err.message } }
  render() {
    if (this.state.error) {
      return (
        <div style={{ padding: 24, color: '#f85149', backgroundColor: '#0d1117', minHeight: '100vh' }}>
          <h2 style={{ fontSize: 14, marginBottom: 8 }}>페이지 오류</h2>
          <pre style={{ fontSize: 12, color: '#8b949e', whiteSpace: 'pre-wrap' }}>{this.state.error}</pre>
          <button
            onClick={() => { this.setState({ error: null }); window.location.hash = '#/hedged' }}
            style={{ marginTop: 12, padding: '6px 16px', backgroundColor: '#21262d', color: '#e6edf3', border: '1px solid #30363d', borderRadius: 4, cursor: 'pointer', fontSize: 12 }}
          >
            돌아가기
          </button>
        </div>
      )
    }
    return this.props.children
  }
}

function App() {
  return (
    <HashRouter>
      <Layout>
        <ErrorBoundary>
          <Routes>
            <Route path="/" element={<Navigate to="/hedged" replace />} />
            <Route path="/hedged" element={<Hedged />} />
            <Route path="/unmatched" element={<Unmatched />} />
            <Route path="/dashboard" element={<Dashboard />} />
            <Route path="/settings" element={<Settings />} />
          </Routes>
        </ErrorBoundary>
      </Layout>
    </HashRouter>
  )
}

export default App
