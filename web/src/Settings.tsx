import { useEffect } from 'react'
import { askAccess, scopeName } from './access'
import { AgentCard, AgentStatus } from './AgentCard'
import { AssistantSettings } from './AssistantSettings'
import { BohemiaMark } from './bohemia'
import { Computers } from './Computers'
import { Glyph } from './glyphs'
import type { GlyphName } from './glyphs'
import { startPrompt, useAgentSetup } from './parts'
import { Group, Row } from './settings-rows'
import { useApp } from './state'
import type { ThemeChoice } from './types'
import { UpdateRow } from './update'
import './settings.css'

/*
 * Settings the way Claude and ChatGPT lay them out: a short list of groups on the left, one group at a time on the
 * right, and every setting a row of "what it is, in a line" beside its control.
 */

const channelText: Record<string, string> = {
  toast: '本机 Windows 通知',
  feishu_app: '飞书应用',
  feishu_webhook: '飞书群机器人',
  webhook: 'Webhook',
}

type PaneID = 'general' | 'notifications' | 'computers' | 'agent' | 'assistant' | 'access'
const panes: { id: PaneID; label: string; glyph?: GlyphName; lead: string }[] = [
  { id: 'general', label: '通用', glyph: 'settings', lead: '外观、版本和更新。' },
  { id: 'notifications', label: '通知', glyph: 'bell', lead: '服务出问题时，lgmon 通知谁、怎么通知。' },
  { id: 'computers', label: '电脑', glyph: 'computer', lead: '被监控的电脑、资源占用和离线缓存。' },
  {
    id: 'agent',
    label: 'Agent',
    glyph: 'agent',
    lead: '让 Claude Code、Codex 等通过 MCP 替你加服务、查问题。',
  },
  {
    id: 'assistant',
    label: '波西米亚',
    lead: '面板里的助手：回答服务状况和 lgmon 怎么用，也能改已有的服务；删除和对外发送之前先问你。',
  },
  { id: 'access', label: '访问权限', glyph: 'key', lead: '这个网页能做什么，以及怎么获得更多权限。' },
]

function readThemeChoice(): ThemeChoice {
  try {
    const saved = localStorage.getItem('lgmon-theme')
    return saved === 'light' || saved === 'dark' ? saved : 'system'
  } catch {
    return 'system'
  }
}

function General({ setTheme }: { setTheme: (theme: ThemeChoice) => void }) {
  const { guide, boards } = useApp()
  const choice = readThemeChoice()
  return (
    <>
      <Group title="外观">
        <Row title="配色" hint="跟随系统，或者固定用浅色、深色。">
          <div className="lm-seg" role="group" aria-label="配色">
            {(
              [
                ['system', '跟随系统', 'computer'],
                ['light', '浅色', 'sun'],
                ['dark', '深色', 'moon'],
              ] as [ThemeChoice, string, GlyphName][]
            ).map(([value, label, glyph]) => (
              <button key={value} aria-pressed={choice === value} onClick={() => setTheme(value)}>
                <Glyph name={glyph} size={15} />
                {label}
              </button>
            ))}
          </div>
        </Row>
      </Group>
      <Group title="关于 lgmon">
        <UpdateRow version={guide?.version || ''} />
        <Row
          title="使用说明"
          hint="安装、添加服务、通知和 Agent 的完整说明"
          href="https://github.com/LFenX/lg-monitor#readme"
          external
        />
      </Group>
      <Group title="更多">
        <Row
          title="自定义视图"
          hint={boards.length ? boards.length + ' 页 Agent 做的图表' : 'Agent 用 put_dashboard 做的图表页'}
          href="#/views"
        />
      </Group>
    </>
  )
}

function Notifications() {
  const { data } = useApp()
  const channels = [...(data?.notifiers.notifiers || [])]
  const feishu = channels.some((channel) => channel.type.startsWith('feishu'))
  return (
    <>
      <Group title="通知渠道">
        {channels.length ? (
          channels.map((channel) => (
            <Row
              key={channel.id}
              title={channelText[channel.type] || channel.type}
              hint={
                channel.chatName || (channel.type === 'toast' ? '服务异常时在这台电脑右下角弹出' : channel.id)
              }
            >
              <span className="set-chip" data-tone="up">
                已启用
              </span>
            </Row>
          ))
        ) : (
          <Row
            title={<a href="#/notifications/setup">还没有设置告警通知</a>}
            hint="加第一个服务时会自动启用本机 Windows 通知。"
          />
        )}
      </Group>
      <Group>
        {feishu ? (
          <Row title="管理通知" hint="渠道、发给哪些规则、暂停通知" href="#/notifications" />
        ) : (
          <Row title="接入飞书" hint="把告警发到手机上的飞书群" href="#/notifications/setup" />
        )}
        <Row title="告警卡片模板" hint="飞书卡片的样式和字段" href="#/notifications" />
      </Group>
    </>
  )
}

function ComputersPane() {
  return (
    <>
      <Computers />
      <Group>
        <Row title="浏览这台电脑" hint="正在监听的端口、Windows 服务和进程" href="#/explore" />
      </Group>
    </>
  )
}

function Agent() {
  const setup = useAgentSetup('all')
  return (
    <>
      <Group title="接入状态">
        <Row title="Agent" hint="Agent 每次通过 MCP 调用 lgmon 都会更新这里。">
          <AgentStatus />
        </Row>
      </Group>
      <AgentCard
        quiet
        status={false}
        title="把这段话发给你的 Agent"
        lead="它会自己接入 lgmon，然后问你想做什么：加服务、配告警、查问题都可以。"
        text={startPrompt(setup, true)}
      />
      <Group>
        <Row title="各客户端的详细接法" hint="Claude Code、Codex、Cursor 等的手动配置" href="#/connect" />
      </Group>
    </>
  )
}

function Access() {
  const { scope } = useApp()
  return (
    <Group
      title="这个网页"
      note="在这台电脑上以你自己的身份运行 lgmon 时，网页本来就能修改；lgmon 以 Windows 服务运行，或者从别的电脑、手机打开时，才需要令牌。遇到被挡住的操作，页面会当场告诉你怎么办。"
    >
      <Row title="现在的权限" hint={'这个网页现在' + scopeName(scope) + '。'}>
        {scope !== 'admin' && (
          <button className="set-btn" onClick={() => askAccess(scope === 'config' ? 'admin' : 'config')}>
            <Glyph name="key" size={15} />
            {scope === 'config' ? '换成管理员令牌' : '获得修改权限'}
          </button>
        )}
      </Row>
    </Group>
  )
}

export function Settings({
  section,
  setTheme,
  openAssistant,
}: {
  section?: string
  setTheme: (theme: ThemeChoice) => void
  openAssistant: () => void
}) {
  const pane = panes.find((item) => item.id === section) || panes[0]
  // Each group starts at the top, like a page of its own.
  useEffect(() => {
    window.scrollTo?.({ top: 0 })
  }, [pane.id])
  return (
    <div className="lm-page lm-settings">
      <header className="lm-head">
        <div>
          <h1 className="lm-title">设置</h1>
        </div>
      </header>
      <div className="set-layout">
        <nav className="set-nav" aria-label="设置分组">
          {panes.map((item) => (
            <a
              key={item.id}
              href={'#/settings/' + item.id}
              aria-current={item.id === pane.id ? 'page' : undefined}
            >
              {item.glyph ? <Glyph name={item.glyph} size={16} /> : <BohemiaMark size={16} />}
              {item.label}
            </a>
          ))}
        </nav>
        <div className="set-pane" role="region" aria-label={pane.label}>
          <header className="set-pane-head">
            <h2>{pane.label}</h2>
            <p>{pane.lead}</p>
          </header>
          {pane.id === 'general' && <General setTheme={setTheme} />}
          {pane.id === 'notifications' && <Notifications />}
          {pane.id === 'computers' && <ComputersPane />}
          {pane.id === 'agent' && <Agent />}
          {pane.id === 'assistant' && <AssistantSettings open={openAssistant} />}
          {pane.id === 'access' && <Access />}
        </div>
      </div>
    </div>
  )
}
