import { AgentCard } from './AgentCard'
import { BrandMark, Wordmark } from './brand'
import { Glyph } from './glyphs'
import { startPrompt, useAgentSetup } from './parts'
import { useApp } from './state'
import { Dot } from './status'
import type { Monitor } from './types'
import './start.css'

/*
 * What opening lgmon shows: a small window that says whether lgmon is running and how the services are, with the
 * message for the agent right there, so the first step is one click. The full board opens in the browser.
 */

const health = (monitor: Monitor) => monitor.overall || monitor.state

// "All clear" only when every service has answered; a fresh service without data yet is said as such.
function headline(loading: boolean, services: Monitor[], down: Monitor[]) {
  if (loading) return '正在读取服务…'
  if (!services.length) return '还没有在监控任何服务'
  if (down.length) return down.length + ' 个服务异常'
  const waiting = services.filter((monitor) => health(monitor) !== 'up').length
  if (waiting) return waiting === services.length ? '正在检查服务…' : waiting + ' 个服务暂时没有数据'
  return '一切正常'
}

export function StartPage() {
  const { data, error, monitors, guide, status } = useApp()
  const setup = useAgentSetup('all')
  const loading = !data || !guide
  const services = monitors.filter((monitor) => !monitor.parent)
  const children = monitors.length - services.length
  const down = services.filter((monitor) => health(monitor) === 'down')
  const firing = data?.alerts.length || 0
  const host = guide?.collectors[0]?.hostname
  // Troubled services first, so the window answers "is anything wrong" at a glance.
  const listed = [...down, ...services.filter((monitor) => !down.includes(monitor))].slice(0, 4)
  const live = status === 'connected'
  const empty = !loading && !services.length
  const facts = [
    services.length ? services.length + ' 个服务' : '',
    children ? children + ' 个子服务' : '',
    firing ? firing + ' 条告警' : '',
    host ? '这台电脑 ' + host : '',
  ].filter(Boolean)
  return (
    <div className="lm-start">
      <header className="lm-start-head">
        <span className="lm-start-brand">
          <BrandMark size={26} />
          <Wordmark />
        </span>
        <span className="lm-start-live" data-live={live || undefined}>
          <i aria-hidden="true" />
          {error && !data ? '连不上 lgmon' : live ? '正在运行' : '连接中'}
        </span>
      </header>

      <section
        className="lm-start-status"
        aria-label="服务概况"
        data-state={down.length ? 'down' : undefined}
      >
        <h1>{headline(loading, services, down)}</h1>
        {facts.length > 0 && <p>{facts.join(' · ')}</p>}
        {listed.length > 0 && (
          <ul>
            {listed.map((monitor) => (
              <li key={monitor.id}>
                <a href={'#/monitors/' + encodeURIComponent(monitor.id)} target="_blank" rel="noreferrer">
                  <Dot state={health(monitor)} />
                  <span className="name">{monitor.name}</span>
                  {health(monitor) === 'down' && monitor.issue && (
                    <span className="issue">{monitor.issue}</span>
                  )}
                </a>
              </li>
            ))}
          </ul>
        )}
      </section>

      <AgentCard
        title={empty ? '让 Agent 帮你加第一个服务' : '有事交给 Agent'}
        lead={
          empty
            ? '把这段话发给 Claude Code、Codex 等 Agent。它会接入 lgmon，然后问你想监控什么。'
            : '加服务、配告警、查问题，直接跟 Agent 说。把这段话发给它就能开始。'
        }
        text={startPrompt(setup, !empty)}
      />

      <footer className="lm-start-foot">
        <a className="lm-btn" href="#/" target="_blank" rel="noreferrer">
          <Glyph name="board" size={16} />
          打开看板
        </a>
        <a className="lm-start-link" href="#/alerts" target="_blank" rel="noreferrer">
          告警
        </a>
        <a className="lm-start-link" href="#/settings" target="_blank" rel="noreferrer">
          设置
        </a>
      </footer>
    </div>
  )
}
