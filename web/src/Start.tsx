import { AgentCard } from './AgentCard'
import { BrandMark, Wordmark } from './brand'
import { Glyph } from './glyphs'
import { startPrompt, useAgentSetup } from './parts'
import { useApp } from './state'
import { Dot } from './status'
import type { Monitor } from './types'
import './start.css'

/*
 * What opening lgmon shows, in lgmon's own window: whether it runs, how the services are, and the one next step.
 * One screen, no scrolling: a headline, the services that matter, the agent card, and the board at the bottom.
 */

const health = (monitor: Monitor) => monitor.overall || monitor.state
const shown = 5

type Mood = 'loading' | 'offline' | 'empty' | 'down' | 'waiting' | 'up'

function mood(loading: boolean, offline: boolean, services: Monitor[], down: Monitor[]): Mood {
  if (offline) return 'offline'
  if (loading) return 'loading'
  if (!services.length) return 'empty'
  if (down.length) return 'down'
  return services.some((monitor) => health(monitor) !== 'up') ? 'waiting' : 'up'
}

// "All clear" only when every service has answered; a fresh service without data yet is said as such.
function headline(state: Mood, services: Monitor[], down: Monitor[]) {
  switch (state) {
    case 'offline':
      return '暂时读不到状态'
    case 'loading':
      return '正在读取…'
    case 'empty':
      return '还没有在监控服务'
    case 'down':
      return down.length + ' 个服务异常'
    case 'waiting': {
      const waiting = services.filter((monitor) => health(monitor) !== 'up').length
      return waiting === services.length ? '正在检查服务…' : waiting + ' 个服务还没有数据'
    }
  }
  return '一切正常'
}

function rowNote(monitor: Monitor) {
  const state = health(monitor)
  if (state === 'down') return monitor.issue || '异常'
  if (state !== 'up') return '还没有数据'
  const uptime = monitor.groupUptime ?? monitor.uptime
  return typeof uptime === 'number' ? (uptime >= 99.95 ? '100%' : uptime.toFixed(1) + '%') : '正常'
}

export function StartPage() {
  const { data, error, monitors, monitorsLoaded, guide, status } = useApp()
  const setup = useAgentSetup('all')
  const offline = !!error && !data
  const loading = !data || !guide || !monitorsLoaded
  const services = monitors.filter((monitor) => !monitor.parent)
  const children = monitors.length - services.length
  const down = services.filter((monitor) => health(monitor) === 'down')
  const firing = data?.alerts.length || 0
  const host = guide?.collectors[0]?.hostname
  const state = mood(loading, offline, services, down)
  // Troubled services first, so the window answers "is anything wrong" at a glance.
  const ordered = [...down, ...services.filter((monitor) => !down.includes(monitor))]
  const listed = ordered.slice(0, shown)
  const live = status === 'connected'
  const empty = state === 'empty'
  const facts =
    state === 'offline'
      ? ['lgmon 可能还在启动，稍后会自动重试']
      : [
          services.length ? services.length + ' 个服务' : '',
          children ? children + ' 个子服务' : '',
          firing ? firing + ' 条告警' : '',
          host ? host : '',
        ].filter(Boolean)
  return (
    <div className="lm-start" data-mood={state}>
      <i className="lm-start-glow" aria-hidden="true" />
      <header className="lm-start-head">
        <span className="lm-start-brand">
          <BrandMark size={28} />
          <Wordmark />
        </span>
        <span className="lm-start-live" data-live={live || undefined}>
          <i aria-hidden="true" />
          {offline ? '连不上' : live ? '运行中' : '连接中'}
        </span>
      </header>

      <main className="lm-start-main">
        <section className="lm-start-hero" aria-label="服务概况">
          <h1>{headline(state, services, down)}</h1>
          {facts.length > 0 && <p>{facts.join(' · ')}</p>}
        </section>

        {listed.length > 0 && (
          <ul className="lm-start-list" aria-label="服务">
            {listed.map((monitor) => (
              <li key={monitor.id} data-state={health(monitor)}>
                <a href={'#/monitors/' + encodeURIComponent(monitor.id)} target="_blank" rel="noreferrer">
                  <Dot state={health(monitor)} />
                  <span className="name">{monitor.name}</span>
                  <span className="note">{rowNote(monitor)}</span>
                </a>
              </li>
            ))}
            {ordered.length > shown && (
              <li className="more">
                <a href="#/" target="_blank" rel="noreferrer">
                  还有 {ordered.length - shown} 个服务，在看板里查看
                </a>
              </li>
            )}
          </ul>
        )}

        <AgentCard
          quiet={!empty}
          title={empty ? '让 Agent 帮你加第一个服务' : '有事交给 Agent'}
          lead={
            empty
              ? '把这段话发给 Claude Code、Codex 等 Agent。它会接入 lgmon，然后问你想监控什么。'
              : '加服务、配告警、查问题，把这段话发给 Agent 就能开始。'
          }
          text={startPrompt(setup, !empty)}
        />
      </main>

      <footer className="lm-start-foot">
        <a className={'lm-start-board' + (empty ? ' soft' : '')} href="#/" target="_blank" rel="noreferrer">
          <Glyph name="board" size={17} />
          打开看板
        </a>
        <nav aria-label="更多">
          <a href="#/alerts" target="_blank" rel="noreferrer">
            告警{firing ? ' ' + firing : ''}
          </a>
          <a href="#/settings" target="_blank" rel="noreferrer">
            设置
          </a>
        </nav>
      </footer>
    </div>
  )
}
