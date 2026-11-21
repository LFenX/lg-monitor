/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import '@testing-library/jest-dom/vitest'
import { afterEach, vi } from 'vitest'
import { cleanup } from '@testing-library/react'

afterEach(cleanup)
Object.defineProperty(window, 'scrollTo', { configurable: true, value: vi.fn() })
