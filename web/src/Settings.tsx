import { useEffect } from 'react'
import { askAccess, scopeName } from './access'
import { AssistantSettings } from './AssistantSettings'
import { Computers } from './Computers'
import { Glyph } from './glyphs'
import type { GlyphName } from './glyphs'
import { ago } from './kit'
import { ConnectSteps } from './parts'
import { useApp } from './state'
import type { Theme } from './types'

const channelText: Record<string, string> = {
  toast: '本机 Windows 通知',
  feishu_app: '飞书应用',
  feishu_webhook: '飞书群机器人',
  webhook: 'Webhook',
}

function Links({ items }: { items: { href: string; glyph: GlyphName; title: string; detail: string }[] }) {
  return (
    <nav className="lm-links">
      {items.map((item) => (
        <a key={item.href} href={item.href}>
          <Glyph name={item.glyph} />
          <span>
            {item.title}
            <small>{item.detail}</small>
          </span>
          <Glyph name="chevron" size={14} />
        </a>
      ))}
    </nav>
  )
}

export function Settings({
  theme,
  setTheme,
  openAssistant,
}: {
  theme: Theme
  setTheme: (theme: Theme) => void
  openAssistant: () => void
}) {
  // Links such as the assistant's "去设置里填写" land on their section.
  useEffect(() => {
    const section = window.location.hash.split('/')[2]
    if (!section) return
    // The shell scrolls to the top on every navigation after this runs, so wait for it.
    const timer = setTimeout(() => document.getElementById(section)?.scrollIntoView({ block: 'start' }), 60)
    return () => clearTimeout(timer)
  }, [])
  const { data, scope, guide, now, boards } = useApp()
  const channels = [...(data?.notifiers.notifiers || [])]
  const feishu = channels.some((channel) => channel.type.startsWith('feishu'))
  return (
    <div className="lm-page">
      <header className="lm-head">
        <div>
          <h1 className="lm-title">设置</h1>
          <p className="lm-lead">通知、Agent 接入和这个网页本身。</p>
        </div>
      </header>
      <div className="lm-sets">
        <section className="lm-set" aria-label="通知">
          <div>
            <h2>通知</h2>
            <p className="lead">服务异常时通知谁。</p>
          </div>
          <div className="content">
            {channels.length ? (
              <dl className="lm-kv">
                {channels.map((channel) => (
                  <div key={channel.id} style={{ display: 'contents' }}>
                    <dt>{channelText[channel.type] || channel.type}</dt>
                    <dd>{channel.chatName || channel.id}</dd>
                  </div>
                ))}
              </dl>
            ) : (
              <p style={{ margin: 0 }}>
                <a href="#/notifications/setup">还没有设置告警通知</a>
                <span className="lm-muted">。加第一个服务时会自动启用本机 Windows 通知。</span>
              </p>
            )}
            <div>
              <a className="lm-btn" href={feishu ? '#/notifications' : '#/notifications/setup'}>
                <Glyph name="bell" size={16} />
                {feishu ? '管理通知' : '接入飞书，把告警发到手机'}
              </a>
            </div>
          </div>
        </section>

        <section className="lm-set" aria-label="电脑">
          <div>
            <h2>电脑</h2>
            <p className="lead">被监控的电脑、资源占用、离线缓存，以及从服务开关导入监控。</p>
          </div>
          <div className="content">
            <Computers />
          </div>
        </section>

        <section className="lm-set" aria-label="接入 Agent">
          <div>
            <h2>接入 Agent</h2>
            <p className="lead">让 Claude Code、Codex 等通过 MCP 替你加监控、查状态。</p>
          </div>
          <div className="content">
            {guide?.state.agentSeenAt ? (
              <p style={{ margin: 0 }}>
                已接入{guide.state.agentName ? '（' + guide.state.agentName + '）' : ''}，最近一次调用在{' '}
                {ago(guide.state.agentSeenAt, now())}。
              </p>
            ) : null}
            <ConnectSteps last="然后直接告诉它要监控什么，例如：帮我监控本机 3000 端口上的博客，和给它做穿透的 cloudflared。" />
            <div>
              <a className="lm-btn ghost small" href="#/connect">
                各客户端的详细接法
                <Glyph name="chevron" size={14} />
              </a>
            </div>
          </div>
        </section>

        <section className="lm-set" aria-label="波西米亚" id="assistant">
          <div>
            <h2>波西米亚</h2>
            <p className="lead">
              lgmon 里的助手：问服务状况，或者 lgmon 怎么用。回答用 DeepSeek，查说明用阿里云百炼的向量模型和
              rerank。只读，不会改动任何东西。
            </p>
          </div>
          <div className="content">
            <AssistantSettings open={openAssistant} />
          </div>
        </section>

        <section className="lm-set" aria-label="网页权限">
          <div>
            <h2>网页权限</h2>
            <p className="lead">这个网页现在{scopeName(scope)}。</p>
          </div>
          <div className="content">
            <p style={{ margin: 0 }}>
              平时用不到这里：在这台电脑上以你自己的身份运行 lgmon
              时，网页本来就能添加和修改服务。只有两种情况需要它：lgmon 以 Windows
              服务运行（本机网页默认只能查看），或者你从别的电脑、手机打开这个网页。遇到被挡住的操作时，页面会当场告诉你怎么办。
            </p>
            {scope !== 'admin' && (
              <div>
                <button className="lm-btn" onClick={() => askAccess(scope === 'config' ? 'admin' : 'config')}>
                  <Glyph name="key" size={16} />
                  {scope === 'config' ? '换成管理员令牌' : '获得修改权限'}
                </button>
              </div>
            )}
          </div>
        </section>

        <section className="lm-set" aria-label="外观">
          <div>
            <h2>外观</h2>
            <p className="lead">默认跟随系统的浅色或深色。</p>
          </div>
          <div className="content">
            <div className="lm-seg" role="group" aria-label="配色">
              <button aria-pressed={theme === 'light'} onClick={() => setTheme('light')}>
                <Glyph name="sun" size={15} />
                浅色
              </button>
              <button aria-pressed={theme === 'dark'} onClick={() => setTheme('dark')}>
                <Glyph name="moon" size={15} />
                深色
              </button>
            </div>
          </div>
        </section>

        <section className="lm-set" aria-label="更多">
          <div>
            <h2>更多</h2>
            <p className="lead">平时用不到的工具。</p>
          </div>
          <div className="content">
            <Links
              items={[
                {
                  href: '#/views',
                  glyph: 'board',
                  title: '自定义视图',
                  detail: boards.length
                    ? boards.length + ' 页 Agent 做的图表'
                    : 'Agent 用 put_dashboard 做的图表页',
                },
                {
                  href: '#/explore',
                  glyph: 'search',
                  title: '浏览这台电脑',
                  detail: '正在监听的端口、Windows 服务和进程',
                },
                {
                  href: '#/notifications',
                  glyph: 'bell',
                  title: '告警卡片模板',
                  detail: '飞书卡片的样式和字段',
                },
              ]}
            />
          </div>
        </section>
      </div>
    </div>
  )
}
