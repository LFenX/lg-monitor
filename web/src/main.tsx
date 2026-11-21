/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App'
import './style.css'
import './redesign.css'
import './lgmon.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
