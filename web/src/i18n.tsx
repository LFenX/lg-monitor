import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'

export type Lang = 'zh' | 'en'

const langKey = 'lgmon-lang'

function initialLang(): Lang {
  try {
    const saved = localStorage.getItem(langKey)
    if (saved === 'zh' || saved === 'en') return saved
  } catch {
    // Ignore private-mode storage errors.
  }
  return 'zh'
}

const dict = {
  zh: {
    // Navigation & shell
    boards: '面板',
    newBoard: '新建面板',
    sharedBoards: '别人的面板',
    alerts: '告警',
    lan: '内网',
    settings: '设置',
    live: '实时',
    connecting: '连接中',
    reconnecting: '重连中',
    search: '搜索',
    searchTitle: '搜索（Ctrl K）',
    toDark: '切换到深色',
    toLight: '切换到浅色',
    bohemia: '波西米亚',
    bohemiaTitle: '波西米亚：问服务状况或 lgmon 怎么用（Ctrl J）',
    bohemiaReadOnly: '只读模式 · 可查询状态与文档',
    langSwitchTitle: '切换语言（中文 / English）',
    // Board
    viewList: '列表',
    viewCards: '卡片',
    viewMap: '关系',
    addService: '添加服务',
    statServices: '服务',
    statChildren: '子服务',
    statUp: '正常',
    statDown: '异常',
    statUptime24h: '24 小时可用率',
    // Start window
    startAllUp: '一切正常',
    startLoading: '正在读取…',
    startOffline: '暂时读不到状态',
    startEmpty: '还没有在监控服务',
    startOpenBoard: '打开看板',
    startRunning: '运行中',
    startDisconnected: '连不上',
    startChooseAvatar: '选择头像',
    startRandomAvatar: '随机换一个头像',
    startEditProfile: '修改头像和昵称',
    startLanDept: '内网与部门',
    startGetAccess: '获得修改权限',
    startForgetToken: '忘掉长期令牌',
    startSharedTitle: '别人的面板',
    startSharedSummary: '共享面板',
    startSharedAllUp: '全部正常',
    startSharedLoading: '正在读取…',
    // Settings
    settingsTitle: '设置',
    paneGeneral: '通用',
    paneGeneralLead: '外观、语言、版本和更新。',
    paneNotifications: '通知',
    paneNotificationsLead: '服务出问题时，lgmon 通知谁、怎么通知。',
    paneComputers: '电脑',
    paneComputersLead: '被监控的电脑、资源占用和离线缓存。',
    paneAgent: 'Agent',
    paneAgentLead: '让 Claude Code、Codex 等通过 MCP 替你加服务、查问题。',
    paneAssistant: '波西米亚',
    paneAssistantLead:
      '面板里的助手：回答服务状况和 lgmon 怎么用，也能改已有的服务；删除和对外发送之前先问你。',
    paneAccess: '访问权限',
    paneAccessLead: '这个网页能做什么，以及怎么获得更多权限。',
    appearance: '外观',
    themeLabel: '配色',
    themeHint: '跟随系统，或者固定用浅色、深色。',
    themeSystem: '跟随系统',
    themeLight: '浅色',
    themeDark: '深色',
    languageLabel: '语言',
    languageHint: '界面显示语言，切换后立即生效。',
    // LAN Web Access & Visitors
    lanWebTitle: '内网浏览器只读访问',
    lanWebHint:
      '打开后，同一局域网或 Tailscale 里的同事可以直接用浏览器打开下方地址只读查看本看板，并可只读使用波西米亚问答；所有修改操作都会被严格拦截。',
    lanWebAddresses: '内网访问地址',
    lanWebOffHint: '当前仅本机可访问；打开后自动监听内网网卡并提供只读访客视图。',
    guestViewingPrefix: '您正在查看',
    guestViewingSuffix: '的 lgmon 监控看板',
    guestReadOnly: '只读',
    guestBohemiaHint: '可用波西米亚查询状态（仅只读权限）',
    guestAskBohemia: '问波西米亚',
    downloadTitle: '获取 lgmon',
    downloadSub: '免费下载 Windows 最新安装包',
    downloadCta: '立即下载最新版',
    visitorsOnline: '位内网访客正在查看',
    visitorsNone: '内网网页已开启 · 暂无访客',
    visitorsTitle: '当前内网访客',
    visitorsEmpty: '目前没有其他设备通过内网浏览器查看你的看板。',
    visitorsLive: '在线',
    visitorsRecent: '刚刚访问',
    visitorsFirstSeen: '首次访问',
    visitorsLastSeen: '最近活跃',
    visitorsShareLinks: '把下面地址发给内网同事即可只读访问',
    visitorsClose: '关闭',
  },
  en: {
    // Navigation & shell
    boards: 'Boards',
    newBoard: 'New board',
    sharedBoards: 'Shared boards',
    alerts: 'Alerts',
    lan: 'LAN',
    settings: 'Settings',
    live: 'Live',
    connecting: 'Connecting',
    reconnecting: 'Reconnecting',
    search: 'Search',
    searchTitle: 'Search (Ctrl K)',
    toDark: 'Switch to dark',
    toLight: 'Switch to light',
    bohemia: 'Bohemia',
    bohemiaTitle: 'Bohemia: ask about services or lgmon (Ctrl J)',
    bohemiaReadOnly: 'Read-only · inspect status & docs',
    langSwitchTitle: 'Switch language (中文 / English)',
    // Board
    viewList: 'List',
    viewCards: 'Cards',
    viewMap: 'Map',
    addService: 'Add service',
    statServices: 'Services',
    statChildren: 'Sub-services',
    statUp: 'Up',
    statDown: 'Down',
    statUptime24h: '24h Uptime',
    // Start window
    startAllUp: 'All systems operational',
    startLoading: 'Loading…',
    startOffline: 'Unable to reach status',
    startEmpty: 'No services monitored yet',
    startOpenBoard: 'Open dashboard',
    startRunning: 'Running',
    startDisconnected: 'Offline',
    startChooseAvatar: 'Choose avatar',
    startRandomAvatar: 'Random avatar',
    startEditProfile: 'Edit avatar & name',
    startLanDept: 'LAN & departments',
    startGetAccess: 'Get edit access',
    startForgetToken: 'Forget saved token',
    startSharedTitle: 'Shared boards',
    startSharedSummary: 'Shared',
    startSharedAllUp: 'all healthy',
    startSharedLoading: 'checking…',
    // Settings
    settingsTitle: 'Settings',
    paneGeneral: 'General',
    paneGeneralLead: 'Appearance, language, version, and updates.',
    paneNotifications: 'Notifications',
    paneNotificationsLead: 'Who lgmon notifies and how when a service has trouble.',
    paneComputers: 'Computers',
    paneComputersLead: 'Monitored hosts, resource usage, and offline cache.',
    paneAgent: 'Agent',
    paneAgentLead: 'Let Claude Code, Codex, and other agents manage monitors over MCP.',
    paneAssistant: 'Bohemia',
    paneAssistantLead: 'Built-in assistant for checking service status, docs, and safe edits.',
    paneAccess: 'Access',
    paneAccessLead: 'What this window can do and how to elevate permissions.',
    appearance: 'Appearance',
    themeLabel: 'Theme',
    themeHint: 'Follow Windows or lock to light or dark.',
    themeSystem: 'System',
    themeLight: 'Light',
    themeDark: 'Dark',
    languageLabel: 'Language',
    languageHint: 'Interface display language; applies immediately.',
    // LAN Web Access & Visitors
    lanWebTitle: 'Read-only LAN Web Access',
    lanWebHint:
      'Allow teammates on the same LAN or Tailscale network to open this dashboard read-only in their browser, including read-only Bohemia Q&A. All write operations are strictly blocked.',
    lanWebAddresses: 'LAN URLs',
    lanWebOffHint: 'Currently localhost-only. Turn on to serve read-only LAN guests.',
    guestViewingPrefix: 'Viewing',
    guestViewingSuffix: "'s lgmon dashboard",
    guestReadOnly: 'Read-only',
    guestBohemiaHint: 'Bohemia available (read-only)',
    guestAskBohemia: 'Ask Bohemia',
    downloadTitle: 'Get lgmon',
    downloadSub: 'Download latest Windows installer',
    downloadCta: 'Download Latest',
    visitorsOnline: 'LAN visitors viewing',
    visitorsNone: 'LAN web active · No visitors yet',
    visitorsTitle: 'Active LAN Visitors',
    visitorsEmpty: 'No other devices on the LAN are currently viewing your dashboard.',
    visitorsLive: 'Live',
    visitorsRecent: 'Recent',
    visitorsFirstSeen: 'First seen',
    visitorsLastSeen: 'Last active',
    visitorsShareLinks: 'Share any address below with teammates for read-only viewing',
    visitorsClose: 'Close',
  },
} as const

export type MessageKey = keyof typeof dict.zh

type I18nValue = {
  lang: Lang
  setLang: (next: Lang) => void
  t: (key: MessageKey) => string
}

const I18nContext = createContext<I18nValue>({
  lang: 'zh',
  setLang: () => {},
  t: (key) => dict.zh[key],
})

export function I18nProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<Lang>(initialLang)
  const setLang = useCallback((next: Lang) => {
    setLangState(next)
    try {
      localStorage.setItem(langKey, next)
    } catch {
      // Ignore storage errors.
    }
  }, [])
  useEffect(() => {
    document.documentElement.lang = lang === 'en' ? 'en' : 'zh-CN'
  }, [lang])
  const value = useMemo<I18nValue>(
    () => ({
      lang,
      setLang,
      t: (key) => dict[lang][key],
    }),
    [lang, setLang],
  )
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>
}

export function useI18n() {
  return useContext(I18nContext)
}
