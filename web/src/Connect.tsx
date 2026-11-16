import { CopyBox, ago, useCopy } from './kit'
import { useApp } from './state'
import { Icon } from './ui'
import { CopyButton, PromptBox, agentSetup, firstPrompt } from './parts'

const prompts = [
  ['看看这台电脑上有什么值得监控，把最重要的几个服务加进面板。', '从零开始'],
  ['帮我监控 3000 端口上的博客，再把给它做穿透的 cloudflared 挂在它下面。', '服务和子服务'],
  ['监控 https://example.com 这个网站，证书快到期也提醒我。', '监控网站'],
  ['监控计划任务 \\Backup\\Nightly，失败了提醒我。', '监控计划任务'],
  ['列出设为自动启动却停着的 Windows 服务，告诉我哪些需要关心。', '体检服务'],
  ['为什么现在有告警？查一下原因和前后发生了什么。', '排查告警'],
] as const

const tools = [
  ['start_here', '了解现状和下一步'],
  ['explore', '搜索端口、服务、进程'],
  ['list_boards · get_board', '看面板和服务树'],
  ['create_board', '按名称创建面板，不需要人先创建'],
  ['add_monitor', '一次调用建好服务、告警，放进面板；parent 挂子服务'],
  ['get_monitor', '单个服务的 24 小时、30 天和事件'],
  ['put_dashboard', '需要图表时做自定义视图'],
  ['list_alerts · get_alert_diagnostics', '排查告警'],
] as const

function Prompt({ text, label, instruction }: { text: string; label: string; instruction: string }) {
  const { copied, copy } = useCopy()
  return (
    <li>
      <button onClick={() => copy(instruction)}>
        <Icon name={copied ? 'check' : 'copy'} size={15} />
        <span>{text}</span>
        <small>{copied ? '已复制' : label}</small>
      </button>
    </li>
  )
}

export function Connect() {
  const { guide, now } = useApp()
  const install = guide?.install
  const quote = (value: string) => "'" + value.replaceAll("'", "''") + "'"
  const command = install
    ? '& ' + quote(install.executable) + ' agent connect --home ' + quote(install.home)
    : 'lgmon agent connect'
  const seen = guide?.state.agentSeenAt || 0
  const setup = agentSetup(install)
  const prompt = firstPrompt('我的服务', '', setup)
  return (
    <div className="fade-in">
      <div className="page-heading">
        <div>
          <p className="eyebrow">CONNECT AN AGENT</p>
          <h1>接入 Agent</h1>
          <p className="muted">
            lgmon 通过 MCP 把能力交给 AI Agent。Claude Code、Codex、Cursor、Cline……任何支持 MCP
            的都可以，接好后用一句话就能加服务、挂子服务。
          </p>
        </div>
      </div>
      <div className="connect-grid">
        <div>
          <section className="step-card" aria-label="交给 Agent 接入">
            <h2>直接交给 Agent（推荐）</h2>
            <p>
              复制整段话，把【】里的任务换成你的需求。Agent
              会自行接入、创建面板并添加服务；需要管理员权限时，由你本人确认 Windows 授权。
            </p>
            <PromptBox text={prompt} />
            <CopyButton text={prompt} primary label="复制完整提示词" />
          </section>
          <details className="step-card">
            <summary>手动接入（可选）</summary>
            <section className="step-card">
              <h2>
                <span className="num">1</span>在这台电脑上运行一条命令
              </h2>
              <p>
                打开 PowerShell，粘贴运行。它会生成一个专用的访问令牌，并打印好各种 Agent 可以直接用的配置。
                {install && !install.personal && (
                  <>
                    <br />
                    <strong style={{ color: 'var(--amber)' }}>
                      监控器以系统服务运行，请先右键“以管理员身份运行” PowerShell。
                    </strong>
                  </>
                )}
              </p>
              <CopyBox text={command} label="接入命令" />
            </section>
            <section className="step-card">
              <h2>
                <span className="num">2</span>按你用的 Agent 照做一项
              </h2>
              <p>
                命令输出里有四种写法：<strong>Claude Code</strong> 和 <strong>Codex</strong>{' '}
                各是一条可以直接运行的命令；
                <strong>Cursor、Cline、Windsurf、Gemini CLI</strong> 等把输出里的 JSON 合并进它的 MCP
                配置；只支持 HTTP 的客户端用输出里的地址和请求头。 令牌只显示这一次，不要发到聊天里。
              </p>
            </section>
          </details>
          <section className="step-card">
            <h2>对 Agent 说一句话</h2>
            <p>点一下就复制。Agent 会先调用 start_here 了解情况，然后自己完成剩下的事。</p>
            <ul className="prompt-list">
              {prompts.map(([text, label]) => (
                <Prompt
                  key={text}
                  text={text}
                  label={label}
                  instruction={firstPrompt('我的服务', '', setup, text)}
                />
              ))}
            </ul>
          </section>
        </div>
        <aside>
          <section className="step-card">
            <h2>
              <i className={'status-dot' + (seen ? ' on' : '')} />
              {seen ? 'Agent 已接入' : '还没有 Agent 连接'}
            </h2>
            <p>
              {seen
                ? (guide?.state.agentName ? '令牌 ' + guide.state.agentName + ' · ' : '') +
                  '最近一次调用在 ' +
                  ago(seen, now()) +
                  '。'
                : '把左侧完整提示词交给 Agent；它第一次调用工具时这里会变绿。'}
            </p>
          </section>
          <section className="step-card">
            <h2>Agent 能做什么</h2>
            <ul className="viz-legend" style={{ marginTop: 10 }}>
              {tools.map(([name, text]) => (
                <li key={name} style={{ gridTemplateColumns: '1fr' }}>
                  <span>
                    <code>{name}</code>
                    <span className="muted"> — {text}</span>
                  </span>
                </li>
              ))}
            </ul>
            <p style={{ marginTop: 12 }}>
              默认提供 21 个常用工具。需要完整管理工具时，把配置里的 <code>--tools essential</code> 改成{' '}
              <code>--tools all</code>。
            </p>
          </section>
          <section className="step-card">
            <h2>安全边界</h2>
            <p>
              令牌默认是 config
              权限：可以加监控、做面板、确认告警，但不能读密钥、不能改系统设置，也不会停止或重启任何已有的服务。不用了可以随时吊销：
            </p>
            <CopyBox
              text={
                (install ? '& ' + quote(install.executable) : 'lgmon') +
                ' token list --home ' +
                (install ? quote(install.home) : '<home>')
              }
            />
          </section>
        </aside>
      </div>
    </div>
  )
}
