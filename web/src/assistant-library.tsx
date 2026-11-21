/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useState } from 'react'
import { useGate } from './access'
import { Avatar } from './avatar'
import { Group, Row } from './settings-rows'
import { useApp, useResource } from './state'

type KnowledgeDoc = { id: string; name: string; vectorize: boolean; passages: number; indexed: number }
type Knowledge = {
  asked: boolean
  guides: number
  guidesIndexed: number
  services: number
  servicesIndexed: number
  docs: KnowledgeDoc[]
}
type Share = { memberId: string; name: string; avatar?: string; shared: boolean }

function Switch({ on, label, busy, flip }: { on: boolean; label: string; busy: boolean; flip: () => void }) {
  return (
    <button
      className="lm-switch"
      role="switch"
      aria-checked={on}
      aria-label={label}
      disabled={busy}
      onClick={flip}
    >
      <i />
    </button>
  )
}

const count = (done: number, all: number) =>
  all ? (done >= all ? all + ' 段，都已向量化' : done + ' / ' + all + ' 段已向量化') : '还没有'

/*
 * What 波西米亚 can look things up in. Guides and service cards are embedded on their own once 百炼 is set up;
 * each document waits for its owner's yes and is searched by keywords until then.
 */
export function KnowledgeSettings() {
  const { api, version } = useApp()
  const gate = useGate()
  const [busy, setBusy] = useState(''),
    [error, setError] = useState('')
  const [tick, setTick] = useState(0)
  const library = useResource(
    (signal) => api.request<Knowledge>('/assistant/knowledge', { signal }),
    [api, version, tick],
  )
  const value = library.value
  if (!value || !Array.isArray(value.docs)) return null
  const choose = (body: Record<string, unknown>, key: string) =>
    gate('config', '选择要向量化的文档', () => {
      setBusy(key)
      setError('')
      api
        .request<Knowledge>('/assistant/knowledge', { method: 'PUT', body: JSON.stringify(body) })
        .then(() => {
          // Embedding runs in the background; read the counts again shortly.
          setTick((n) => n + 1)
          setTimeout(() => setTick((n) => n + 1), 2500)
        })
        .catch((err: unknown) => setError(err instanceof Error ? err.message : '保存失败'))
        .finally(() => setBusy(''))
    })
  return (
    <Group
      title="知识库"
      note={
        <>
          波西米亚按意思和关键词一起检索。服务文档默认只按关键词检索；打开后才做向量化，之后文档改动时只重新向量化改了的段落，删掉的段落也从索引里去掉。
          {error && <span className="lm-error"> {error}</span>}
        </>
      }
    >
      <Row title="lgmon 使用说明" hint={count(value.guidesIndexed, value.guides)} />
      <Row
        title="服务资料"
        hint={
          '每个服务一段：说明、角色、监控方式、访问入口和依赖。' +
          count(value.servicesIndexed, value.services)
        }
      />
      {value.docs.length > 1 && (
        <Row title="全部服务文档" hint="一次打开或关闭">
          <button className="set-btn" disabled={!!busy} onClick={() => choose({ all: true }, 'all')}>
            全部向量化
          </button>
          <button className="set-btn" disabled={!!busy} onClick={() => choose({ all: false }, 'none')}>
            全部只用关键词
          </button>
        </Row>
      )}
      {value.docs.map((doc) => (
        <Row
          key={doc.id}
          title={'文档：' + doc.name}
          hint={doc.vectorize ? count(doc.indexed, doc.passages) : doc.passages + ' 段，只按关键词检索'}
        >
          <Switch
            on={doc.vectorize}
            label={'把「' + doc.name + '」的文档纳入向量检索'}
            busy={!!busy}
            flip={() => choose({ docs: { [doc.id]: !doc.vectorize } }, doc.id)}
          />
        </Row>
      ))}
      {!value.docs.length && <Row title="服务文档" hint="还没有服务写了文档。" />}
    </Group>
  )
}

/*
 * Sharing the model with colleagues who are already paired through a board: their 波西米亚 then talks through this
 * computer, which adds the key on the way out. The key itself never leaves.
 */
export function ShareSettings({ own }: { own: boolean }) {
  const { api, version } = useApp()
  const gate = useGate()
  const [busy, setBusy] = useState(''),
    [error, setError] = useState(''),
    [tick, setTick] = useState(0)
  const shares = useResource(
    (signal) => api.request<Share[]>('/assistant/shares', { signal }).catch(() => [] as Share[]),
    [api, version, tick],
  )
  const list = Array.isArray(shares.value) ? shares.value : []
  return (
    <Group
      title="共享给内网同事"
      note={
        own
          ? '打开后，对方的波西米亚没有自己的密钥也能用你的模型：请求先到你的电脑，由你的 lgmon 加上密钥发给 DeepSeek，密钥不会离开这台电脑；对方的窗口顶部会显示是你共享的。关掉后对方下一次提问就停止。'
          : '先在上面配好自己的 DeepSeek 密钥，才能共享给同事。'
      }
    >
      {list.length ? (
        list.map((item) => (
          <Row
            key={item.memberId}
            title={
              <span className="lm-person">
                <Avatar value={item.avatar} seed={item.name} size={26} />
                <span>
                  <strong>{item.name}</strong>
                </span>
              </span>
            }
            hint={item.shared ? '可以用你的模型' : '没有共享'}
          >
            <Switch
              on={item.shared}
              label={'把模型共享给 ' + item.name}
              busy={!own || busy === item.memberId}
              flip={() =>
                gate('config', '共享模型', () => {
                  setBusy(item.memberId)
                  setError('')
                  api
                    .request('/assistant/shares/' + encodeURIComponent(item.memberId), {
                      method: 'PUT',
                      body: JSON.stringify({ shared: !item.shared }),
                    })
                    .then(() => setTick((n) => n + 1))
                    .catch((err: unknown) => setError(err instanceof Error ? err.message : '保存失败'))
                    .finally(() => setBusy(''))
                })
              }
            />
          </Row>
        ))
      ) : (
        <Row title="还没有同事" hint="同事在“内网”页申请查看你的面板、你同意之后，就能在这里共享给他。" />
      )}
      {error && <p className="lm-error">{error}</p>}
    </Group>
  )
}
