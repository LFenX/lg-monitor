/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { CopyBox, useCopy } from './kit'
import { useI18n } from './i18n'
import { useApp } from './state'
import { Icon } from './ui'
import { AgentCard } from './AgentCard'
import { agentSetup, firstPrompt } from './parts'

const prompts = [
  ['看看这台电脑上有什么值得监控，把最重要的几个服务加进面板。', '从零开始'],
  ['帮我监控 3000 端口上的博客，再把给它做穿透的 cloudflared 挂在它下面。', '服务和子服务'],
  ['监控 https://example.com 这个网站，证书快到期也提醒我。', '监控网站'],
  ['监控计划任务 \\Backup\\Nightly，失败了提醒我。', '监控计划任务'],
  ['列出设为自动启动却停着的 Windows 服务，告诉我哪些需要关心。', '体检服务'],
  ['为什么现在有告警？查一下原因和前后发生了什么。', '排查告警'],
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
  const { guide } = useApp()
  const { lang, t } = useI18n()
  const install = guide?.install
  const quote = (value: string) => "'" + value.replaceAll("'", "''") + "'"
  const command = install
    ? '& ' + quote(install.executable) + ' agent connect --home ' + quote(install.home)
    : 'lgmon agent connect'
  const setup = agentSetup(install, 'all', lang)
  const boardName = t('defaultBoardName')
  const prompt = firstPrompt(boardName, '', setup, '', lang)
  return (
    <div className="fade-in">
      <div className="page-heading">
        <div>
          <p className="eyebrow">CONNECT AN AGENT</p>
          <h1>{t('connectTitle')}</h1>
          <p className="muted">{t('connectLead')}</p>
        </div>
      </div>
      <div className="connect-grid">
        <div>
          <AgentCard title={t('connectAgentTitle')} lead={t('connectAgentLead')} text={prompt} />
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
                  instruction={firstPrompt(boardName, '', setup, text, lang)}
                />
              ))}
            </ul>
          </section>
        </div>
        <aside>
          <section className="step-card">
            <h2>Agent 能做什么</h2>
            {/* The same module map the agent reads when it connects. */}
            <ul className="viz-legend" style={{ marginTop: 10 }}>
              {(guide?.modules || []).map((module) => (
                <li key={module.name} style={{ gridTemplateColumns: '1fr' }} title={module.purpose}>
                  <span>
                    <strong>{module.name}</strong>
                    <span className="muted">
                      {' '}
                      — {module.tools.slice(0, 3).join('、')}
                      {module.tools.length > 3 ? ' 等 ' + module.tools.length + ' 个工具' : ''}
                    </span>
                  </span>
                </li>
              ))}
            </ul>
            <p style={{ marginTop: 12 }}>
              默认提供全部 85 个工具，Agent 可以接管 lgmon 的每个模块：告警、飞书、图表、内网共享和助手。
              能力较弱的 Agent 可以改用 <code>--tools essential</code>，只留 22 个常用工具。
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
