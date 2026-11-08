import { defineConfig } from '@playwright/test'

const address = process.env.LGMON_BROWSER_URL
const suite = process.env.LGMON_BROWSER_SUITE
if (
  suite &&
  !['feishu.spec.ts', 'dashboards.spec.ts', 'collector-config.spec.ts', 'certificates.spec.ts'].includes(
    suite,
  )
)
  throw new Error('未知的浏览器测试组')
if (!address || new URL(address).hostname !== '127.0.0.1') {
  throw new Error('浏览器测试只接受 Go 测试创建的 127.0.0.1 临时服务，请运行 TestNativeWebPanelBrowser')
}

export default defineConfig({
  testDir: './tests',
  testMatch: suite ? [suite] : ['panel.spec.ts', 'templates.spec.ts', 'pause.spec.ts'],
  timeout: 30000,
  globalTimeout: 120000,
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: 'line',
  use: {
    baseURL: process.env.LGMON_BROWSER_URL,
    browserName: 'chromium',
    channel: 'msedge',
    headless: true,
    locale: 'zh-CN',
    timezoneId: 'Asia/Shanghai',
    viewport: { width: 1440, height: 1000 },
    launchOptions: { args: ['--disable-component-update', '--disable-breakpad', '--disable-crash-reporter'] },
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
})
