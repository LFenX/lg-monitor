import { useEffect, useRef, useState } from 'react'
import { useGate } from './access'
import { BohemiaMark } from './bohemia'
import { AgentCard } from './AgentCard'
import { CopyButton, useAgentSetup } from './parts'
import { useApp, useResource } from './state'
import type { AssistantStatus } from './types'

/*
 * Where 波西米亚 gets its two models. Each part is a row with its own state, its key, its model and one button
 * that saves only after a small test, so a wrong key is caught here rather than mid-conversation.
 */

export const assistantPrompt =
  '请用 lgmon 的 MCP 工具帮我配置面板里的助手“波西米亚”：先调用 get_assistant 看还差什么。' +
  '它需要 DeepSeek 的 API Key（回答问题）和阿里云百炼的 API Key（向量模型和 rerank，用来查 lgmon 的说明）。' +
  '告诉我去哪里创建这两个密钥；我把密钥发给你后，用 configure_assistant 保存并让它验证，不要把密钥写进文件、日志或聊天记录以外的地方。' +
  '保存后再调用一次 get_assistant 确认已就绪。'

type Part = 'deepseek' | 'dashscope'

function State({ ready, text }: { ready: boolean; text: string }) {
  return (
    <span className="lm-ai-state" data-ready={ready || undefined}>
      <i />
      {text}
    </span>
  )
}

export function AssistantSettings({ open }: { open: () => void }) {
  const { api } = useApp()
  const gate = useGate()
  const setup = useAgentSetup('all')
  const status = useResource((signal) => api.request<AssistantStatus>('/assistant', { signal }), [api])
  // A monitor from before 波西米亚 answers without these fields; treat that as unavailable rather than crash.
  const value = status.value && Array.isArray(status.value.models) ? status.value : undefined
  const [deepseekKey, setDeepseekKey] = useState(''),
    [model, setModel] = useState(''),
    [dashscopeKey, setDashscopeKey] = useState(''),
    [region, setRegion] = useState<'beijing' | 'singapore'>('beijing'),
    [embedding, setEmbedding] = useState(''),
    [rerank, setRerank] = useState(''),
    [busy, setBusy] = useState<Part | ''>(''),
    [result, setResult] = useState<{ part: Part; ok: boolean; text: string } | null>(null)
  const filled = useRef(false)
  useEffect(() => {
    if (!value || filled.current) return
    filled.current = true
    setModel(value.model)
    setRegion(value.region)
    setEmbedding(value.embeddingModel)
    setRerank(value.rerankModel)
  }, [value])
  const save = (part: Part) =>
    gate('config', '配置波西米亚', () => {
      const body: Record<string, string> =
        part === 'deepseek'
          ? { model: model.trim() }
          : { region, embeddingModel: embedding.trim(), rerankModel: rerank.trim() }
      if (part === 'deepseek' && deepseekKey.trim()) body.deepseekKey = deepseekKey.trim()
      if (part === 'dashscope' && dashscopeKey.trim()) body.dashscopeKey = dashscopeKey.trim()
      setBusy(part)
      setResult(null)
      api
        .request<AssistantStatus>('/assistant', { method: 'PUT', body: JSON.stringify(body) })
        .then((next) => {
          setResult({
            part,
            ok: true,
            text:
              part === 'deepseek'
                ? '已连通 DeepSeek · ' +
                  next.model +
                  (next.models.length ? '（账号可用 ' + next.models.length + ' 个模型）' : '')
                : '已连通百炼 · 向量模型和 rerank 都能用',
          })
          if (part === 'deepseek') setDeepseekKey('')
          else setDashscopeKey('')
          status.reload()
        })
        .catch((err: unknown) =>
          setResult({ part, ok: false, text: err instanceof Error ? err.message : '保存失败' }),
        )
        .finally(() => setBusy(''))
    })
  if (!value)
    return (
      <p className="lm-muted lm-small">
        {status.error ||
          (status.value ? '这个监控器版本还不支持波西米亚，请升级后再配置。' : '正在读取波西米亚的设置…')}
      </p>
    )
  const models = value.models.length ? value.models : [value.model]
  const note = (part: Part) =>
    result?.part === part && (
      <p className={result.ok ? 'lm-ai-ok' : 'lm-error'} role={result.ok ? 'status' : 'alert'}>
        {result.text}
      </p>
    )
  return (
    <div className="lm-ai-settings">
      <div className="lm-ai-row" aria-label="DeepSeek">
        <header>
          <strong>DeepSeek</strong>
          <span>回答问题</span>
          <State ready={value.deepseekKey} text={value.deepseekKey ? '密钥已保存' : '还没有密钥'} />
        </header>
        <form
          onSubmit={(event) => {
            event.preventDefault()
            save('deepseek')
          }}
        >
          <label className="lm-field">
            API Key
            <input
              className="lm-input"
              type="password"
              autoComplete="off"
              spellCheck={false}
              value={deepseekKey}
              onChange={(event) => setDeepseekKey(event.target.value)}
              placeholder={value.deepseekKey ? '已保存；要换时粘贴新的' : '粘贴以 sk- 开头的密钥'}
            />
          </label>
          <label className="lm-field">
            模型
            <select className="lm-input" value={model} onChange={(event) => setModel(event.target.value)}>
              {models.map((item) => (
                <option key={item} value={item}>
                  {item}
                </option>
              ))}
            </select>
          </label>
          <div className="actions">
            <a href="https://platform.deepseek.com/api_keys" target="_blank" rel="noreferrer">
              在 DeepSeek 开放平台创建密钥 ↗
            </a>
            <button
              className="lm-btn primary"
              disabled={busy !== '' || (!value.deepseekKey && !deepseekKey.trim())}
            >
              {busy === 'deepseek' ? '正在测试…' : '保存并测试'}
            </button>
          </div>
          {!value.models.length && value.deepseekKey && (
            <p className="lm-muted lm-small" style={{ margin: 0 }}>
              暂时读不到账号的模型列表，先用默认的 {value.model}。
            </p>
          )}
          {note('deepseek')}
        </form>
      </div>
      <div className="lm-ai-row" aria-label="百炼">
        <header>
          <strong>阿里云百炼</strong>
          <span>查 lgmon 的说明（向量模型 + rerank）</span>
          <State ready={value.dashscopeKey} text={value.dashscopeKey ? '密钥已保存' : '还没有密钥'} />
        </header>
        <form
          onSubmit={(event) => {
            event.preventDefault()
            save('dashscope')
          }}
        >
          <label className="lm-field">
            API Key
            <input
              className="lm-input"
              type="password"
              autoComplete="off"
              spellCheck={false}
              value={dashscopeKey}
              onChange={(event) => setDashscopeKey(event.target.value)}
              placeholder={value.dashscopeKey ? '已保存；要换时粘贴新的' : '粘贴百炼控制台里的 API Key'}
            />
          </label>
          <div className="lm-field">
            地域
            <div className="lm-seg" role="group" aria-label="百炼地域">
              <button
                type="button"
                title="国内站：dashscope.aliyuncs.com"
                aria-pressed={region === 'beijing'}
                onClick={() => setRegion('beijing')}
              >
                北京
              </button>
              <button
                type="button"
                aria-pressed={region === 'singapore'}
                onClick={() => setRegion('singapore')}
                title="国际站：dashscope-intl.aliyuncs.com"
              >
                新加坡
              </button>
            </div>
          </div>
          <details className="lm-ai-models">
            <summary>
              模型：{value.embeddingModel} · {value.rerankModel}（一般不用改）
            </summary>
            <label className="lm-field">
              向量模型
              <input
                className="lm-input"
                value={embedding}
                onChange={(event) => setEmbedding(event.target.value)}
              />
            </label>
            <label className="lm-field">
              rerank 模型
              <input
                className="lm-input"
                value={rerank}
                onChange={(event) => setRerank(event.target.value)}
              />
            </label>
          </details>
          <div className="actions">
            <a href="https://bailian.console.aliyun.com/" target="_blank" rel="noreferrer">
              在百炼控制台创建密钥 ↗
            </a>
            <button
              className="lm-btn primary"
              disabled={busy !== '' || (!value.dashscopeKey && !dashscopeKey.trim())}
            >
              {busy === 'dashscope' ? '正在测试…' : '保存并测试'}
            </button>
          </div>
          {value.docs > 0 && (
            <p className="lm-muted lm-small" style={{ margin: 0 }}>
              已为 {value.docs} 段说明建好索引。
            </p>
          )}
          {note('dashscope')}
        </form>
      </div>
      <AgentCard
        art={false}
        status={false}
        title="让 Agent 帮我配置"
        lead="它会告诉你去哪里创建这两把密钥，你发给它后由它保存并测试。密钥只在保存时经过，不会被读出来。"
        text={setup + assistantPrompt}
      />
      <details className="lm-steps-wrap">
        <summary>在这台电脑的命令行里配置</summary>
        <div className="lm-code">
          <code>{value.setup}</code>
          <CopyButton text={value.setup} />
        </div>
        <p className="lm-muted lm-small">
          按提示依次粘贴 DeepSeek 和百炼的密钥（输入不显示，直接回车沿用已保存的）。
        </p>
      </details>
      <div>
        <button className="lm-btn" onClick={open}>
          <BohemiaMark size={18} />
          {value.configured ? '问波西米亚' : '打开波西米亚'}
        </button>
      </div>
    </div>
  )
}
