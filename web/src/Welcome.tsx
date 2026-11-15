import { useRef, useState } from 'react'
import { BrandMark } from './brand'
import { Glyph } from './glyphs'
import { useGate } from './access'
import { ConnectSteps, CopyButton, Dialog, PromptBox, firstPrompt, useAgentSetup } from './parts'
import { useApp } from './state'
import type { BoardView } from './types'

function useCreateBoard(complete?: () => void) {
  const { api, refresh } = useApp()
  const [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  const pending = useRef(false)
  const gate = useGate()
  const create = (name: string, description = '') => gate('config', '新建面板', () => send(name, description))
  const send = (name: string, description: string) => {
    if (pending.current) return
    pending.current = true
    setBusy(true)
    setError('')
    api
      .request<BoardView>('/boards', {
        method: 'POST',
        body: JSON.stringify({ name: name.trim(), description: description.trim() }),
      })
      .then((board) => {
        refresh()
        window.location.hash = '#/boards/' + encodeURIComponent(board.id)
        complete?.()
      })
      .catch((err: unknown) => setError(err instanceof Error ? err.message : '新建面板失败'))
      .finally(() => {
        pending.current = false
        setBusy(false)
      })
  }
  return { busy, error, create }
}

/* The very first screen: one board is all most people need, so it is a single question. */
export function Welcome() {
  const [name, setName] = useState('我的服务')
  const { busy, error, create } = useCreateBoard()
  const setup = useAgentSetup()
  const prompt = firstPrompt(name, '')
  return (
    <div className="lm-page">
      <div className="lm-welcome">
        <div>
          <BrandMark size={48} />
          <h1>欢迎使用 lgmon</h1>
          <p>把你关心的服务放进一个面板：网站、接口、Windows 服务、计划任务，挂了第一时间知道。</p>
          <form
            onSubmit={(event) => {
              event.preventDefault()
              create(name)
            }}
          >
            <input
              className="lm-input"
              aria-label="面板名称"
              required
              maxLength={40}
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
            <button className="lm-btn primary" disabled={busy}>
              新建面板
            </button>
          </form>
          {error && <p className="lm-error">{error}</p>}
          <div className="lm-path primary" style={{ width: '100%', textAlign: 'left', marginTop: 18 }}>
            <header>
              <Glyph name="agent" />
              <strong>或者直接让 Agent 来</strong>
            </header>
            <p>它会顺手建好面板。把【】里换成你要监控的服务：</p>
            <PromptBox text={prompt} preface={setup} />
            <div className="lm-prompt-bar">
              <span />
              <CopyButton text={setup + prompt} primary label="复制这段话" />
            </div>
            <ConnectSteps compact />
          </div>
        </div>
      </div>
    </div>
  )
}

export function NewBoard({ close }: { close: () => void }) {
  const [name, setName] = useState(''),
    [description, setDescription] = useState('')
  const { busy, error, create } = useCreateBoard(close)
  const setup = useAgentSetup()
  const prompt = firstPrompt(name, description)
  return (
    <Dialog
      title="新建面板"
      close={close}
      footer={
        <>
          {error && <p className="lm-error">{error}</p>}
          <button className="lm-btn" onClick={close}>
            取消
          </button>
          <button className="lm-btn primary" form="lm-new-board" disabled={busy}>
            新建
          </button>
        </>
      }
    >
      <p className="lm-muted" style={{ margin: 0 }}>
        一般一个面板就够了。想把另一台电脑或另一组服务分开看时，再建一个。
      </p>
      <form
        id="lm-new-board"
        style={{ display: 'grid', gap: 14 }}
        onSubmit={(event) => {
          event.preventDefault()
          create(name, description)
        }}
      >
        <label className="lm-field">
          名称
          <input
            className="lm-input"
            required
            maxLength={40}
            placeholder="例如：构建机"
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <label className="lm-field">
          简介（可选）
          <input
            className="lm-input"
            maxLength={200}
            value={description}
            onChange={(event) => setDescription(event.target.value)}
          />
        </label>
      </form>
      <section aria-label="交给 Agent 创建面板">
        <p className="lm-muted">也可以把下面这段话交给 Agent，由它完成接入、创建面板和添加服务。</p>
        <PromptBox text={prompt} preface={setup} />
        <div className="lm-prompt-bar">
          <span />
          <CopyButton text={setup + prompt} primary label="交给 Agent 创建" />
        </div>
        <ConnectSteps compact />
      </section>
    </Dialog>
  )
}
