import { CopyBox, ago, useCopy } from './kit'
import { useApp } from './state'
import { Icon } from './ui'

const prompts = [
  ['看看这台电脑上有什么值得监控，把最重要的几个加上监控，再做一页总览面板。', '从零开始'],
  ['帮我监控 3000 端口上的服务，挂了就提醒我，再给它做一页面板。', '监控一个端口'],
  ['监控 https://example.com 这个网站，证书快到期也提醒我。', '监控网站'],
  ['列出设为自动启动却停着的 Windows 服务，告诉我哪些需要关心。', '体检服务'],
  ['做一页终端风格的面板：大号 CPU 和内存仪表、磁盘排行、最近告警。', '定制面板'],
  ['为什么现在有告警？查一下原因和前后发生了什么。', '排查告警'],
] as const

const tools = [
  ['start_here', '了解现状和下一步'],
  ['explore', '搜索端口、服务、进程'],
  ['add_monitor', '一次调用建好监控、告警、面板'],
  ['create_dashboard', '按 7 个模板出面板'],
  ['put_dashboard', '自由组合 13 种卡片'],
  ['list_alerts · get_alert_diagnostics', '排查告警'],
] as const

function Prompt({ text, label }: { text: string; label: string }) {
  const { copied, copy } = useCopy()
  return (
    <li>
      <button onClick={() => copy(text)}>
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
  return (
    <div className="fade-in">
      <div className="page-heading">
        <div>
          <p className="eyebrow">CONNECT AN AGENT</p>
          <h1>接入 Agent</h1>
          <p className="muted">
            lg-monitor 通过 MCP 把能力交给 AI Agent。Claude Code、Codex、Cursor、Cline……任何支持 MCP
            的都可以，接好后用一句话就能加监控、做面板。
          </p>
        </div>
      </div>
      <div className="connect-grid">
        <div>
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
          <section className="step-card">
            <h2>
              <span className="num">3</span>对 Agent 说一句话
            </h2>
            <p>点一下就复制。Agent 会先调用 start_here 了解情况，然后自己完成剩下的事。</p>
            <ul className="prompt-list">
              {prompts.map(([text, label]) => (
                <Prompt key={text} text={text} label={label} />
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
                : '完成左边的步骤后，Agent 第一次调用工具时这里会变绿。'}
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
              默认只提供 19 个常用工具，再“笨”的 Agent 也不会选错。需要全部 65 个管理工具时，把配置里的{' '}
              <code>--tools essential</code> 改成 <code>--tools all</code>。
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
