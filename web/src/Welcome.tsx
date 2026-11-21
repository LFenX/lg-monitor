/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useRef, useState } from 'react'
import { BrandMark } from './brand'
import { useI18n } from './i18n'
import { useGate } from './access'
import { AgentCard } from './AgentCard'
import { Dialog, firstPrompt, useAgentSetup } from './parts'
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

/*
 * The very first screen. Sending one message to an agent is the whole setup, so that comes first; building the
 * board by hand stays right below for anyone who prefers it.
 */
export function Welcome() {
  const { lang, t } = useI18n()
  const [name, setName] = useState(() => t('defaultBoardName'))
  const { busy, error, create } = useCreateBoard()
  const setup = useAgentSetup()
  return (
    <div className="lm-page">
      <div className="lm-welcome">
        <div>
          <BrandMark size={48} />
          <h1>{t('welcomeTitle')}</h1>
          <p className="lead">{t('welcomeLead')}</p>
          <AgentCard
            title={t('welcomeAgentTitle')}
            lead={t('welcomeAgentLead')}
            text={firstPrompt(name, '', setup, '', lang)}
          />
          <form
            className="lm-welcome-manual"
            onSubmit={(event) => {
              event.preventDefault()
              create(name)
            }}
          >
            <span>{t('welcomeManualHint')}</span>
            <input
              className="lm-input"
              aria-label={t('welcomeBoardNameAria')}
              required
              maxLength={40}
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
            <button className="lm-btn" disabled={busy}>
              {t('newBoard')}
            </button>
          </form>
          {error && <p className="lm-error">{error}</p>}
        </div>
      </div>
    </div>
  )
}

export function NewBoard({ close }: { close: () => void }) {
  const { lang, t } = useI18n()
  const [name, setName] = useState(''),
    [description, setDescription] = useState('')
  const { busy, error, create } = useCreateBoard(close)
  const setup = useAgentSetup()
  return (
    <Dialog
      title={t('newBoard')}
      close={close}
      footer={
        <>
          {error && <p className="lm-error">{error}</p>}
          <button className="lm-btn" onClick={close}>
            {t('cancel')}
          </button>
          <button className="lm-btn primary" form="lm-new-board" disabled={busy}>
            {t('newBoardCreate')}
          </button>
        </>
      }
    >
      <p className="lm-muted" style={{ margin: 0 }}>
        {t('newBoardHint')}
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
          {t('boardNameLabel')}
          <input
            className="lm-input"
            required
            maxLength={40}
            placeholder={t('newBoardPlaceholder')}
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <label className="lm-field">
          {t('newBoardDescOptional')}
          <input
            className="lm-input"
            maxLength={200}
            value={description}
            onChange={(event) => setDescription(event.target.value)}
          />
        </label>
      </form>
      <AgentCard
        art={false}
        title={t('newBoardAgentTitle')}
        lead={t('newBoardAgentLead')}
        text={firstPrompt(name, description, setup, '', lang)}
      />
    </Dialog>
  )
}
