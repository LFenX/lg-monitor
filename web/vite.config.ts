import { defineConfig } from 'vitest/config'

export default defineConfig({
  server: { proxy: { '/api': { target: 'http://127.0.0.1:7444' } } },
  build: { target: 'es2022' },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
    restoreMocks: true,
    clearMocks: true,
  },
})
