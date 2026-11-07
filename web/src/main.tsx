import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { AuthenticationGate } from './components/AuthenticationGate'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <AuthenticationGate><App /></AuthenticationGate>
  </StrictMode>,
)
