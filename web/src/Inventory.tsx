import { quoteSQL } from './api'
import { timestamp } from './model'
import { useApp, useResource } from './state'
import { Empty, ErrorNotice, Panel } from './ui'
import './inventory.css'

type Entry = {
  kind: string
  scope: string
  id: string
  name: string
  ts: number
  detail: string
}
type Scope = { kind: string; scope: string; ts: number; last_success: number; error_code: string }
type Change = { ts: number; scope: string; name: string; change: string }
type Task = {
  enabled: boolean
  state: string
  lastRun: number
  nextRun: number
  lastResult: number
  missedRuns: number
}

const errors: Record<string, string> = {
  read_failed: '读取失败，保留上次数据',
  source_unavailable: '来源暂不可读，保留上次数据',
  limit_exceeded: '清单超出限额，保留上次数据',
  unsupported: '采集器系统不支持',
}
export function taskResult(task: Task) {
  if (!task.lastRun) return '从未运行'
  const hex = '0x' + task.lastResult.toString(16).toUpperCase().padStart(8, '0')
  return task.lastResult === 0 ? '成功 · ' + hex : hex
}

export function Inventory({ collector }: { collector: string }) {
  const { api, version } = useApp()
  const source = useResource(
    async (signal) => {
      const where = ' WHERE collector_id=' + quoteSQL(collector)
      const [entries, scopes, changes] = await Promise.all([
        api.query<Entry>(
          'SELECT kind,scope,id,name,ts,detail FROM inventory_current' +
            where +
            ' ORDER BY kind,scope,id LIMIT 1001',
          signal,
        ),
        api.query<Scope>(
          'SELECT kind,scope,ts,last_success,error_code FROM inventory_scopes' +
            where +
            ' ORDER BY kind,scope',
          signal,
        ),
        api.query<Change>(
          'SELECT ts,scope,name,change FROM inventory_changes' +
            where +
            ' ORDER BY ts DESC,seq DESC,id LIMIT 100',
          signal,
        ),
      ])
      return { entries, scopes, changes }
    },
    [api, collector, version],
    collector,
  )
  const entries = source.value?.entries.slice(0, 1000) || []
  const scopes = source.value?.scopes || []
  const tasks = entries.filter((entry) => entry.kind === 'tasks')
  const startup = entries.filter((entry) => entry.kind === 'startup')
  const problems = scopes.filter((scope) => scope.error_code)
  return (
    <div className="inventory-section">
      <ErrorNotice message={source.error} />
      <Panel title="启动项与计划任务" className="inventory-panel">
        <p className="muted small">只读采集；启动项仅展示登记信息，任务结果保留系统原始代码。</p>
        {!scopes.length && (
          <Empty title={source.loading ? '正在读取清单' : '尚未收到启动项或计划任务数据'}>
            请使用支持此功能的采集器；没有数据不表示不存在启动项。
          </Empty>
        )}
        {!!problems.length && (
          <div className="notice notice-warning" role="status">
            <span>{problems.length} 个来源读取失败，以下保留已知数据；请查看最近成功时间。</span>
          </div>
        )}
        {!!scopes.length && (
          <details>
            <summary>采集覆盖（{scopes.length} 个来源）</summary>
            <div className="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th>类别 / 来源</th>
                    <th>状态</th>
                    <th>最近成功</th>
                  </tr>
                </thead>
                <tbody>
                  {scopes.map((scope) => (
                    <tr key={scope.kind + scope.scope}>
                      <td className="mono small">
                        {scope.kind} / {scope.scope}
                      </td>
                      <td>{errors[scope.error_code] || '最近一次读取成功'}</td>
                      <td>{scope.last_success ? timestamp(scope.last_success) : '尚无成功记录'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </details>
        )}
        {(source.value?.entries.length || 0) > 1000 && (
          <p className="notice">页面展示前 1000 项；完整清单可通过只读 SQL 查询。</p>
        )}
        <h3>计划任务（{tasks.length}）</h3>
        {tasks.length ? (
          <div className="table-scroll inventory-task-table">
            <table>
              <thead>
                <tr>
                  <th>任务</th>
                  <th>状态</th>
                  <th>上次运行 / 结果</th>
                  <th>下次运行</th>
                  <th>遗漏次数</th>
                </tr>
              </thead>
              <tbody>
                {tasks.map((entry) => {
                  const task = JSON.parse(entry.detail) as Task
                  const states: Record<string, string> = {
                    unknown: '未知',
                    disabled: '已禁用',
                    queued: '排队中',
                    ready: '就绪',
                    running: '运行中',
                  }
                  return (
                    <tr key={entry.scope + entry.id}>
                      <td>
                        <strong>{entry.name}</strong>
                        <p className="mono small muted">{entry.id}</p>
                      </td>
                      <td>{!task.enabled ? '已禁用' : states[task.state] || task.state}</td>
                      <td>
                        {task.lastRun ? timestamp(task.lastRun) : '—'}
                        <p className="mono small">{taskResult(task)}</p>
                      </td>
                      <td>{task.nextRun ? timestamp(task.nextRun) : '未安排'}</td>
                      <td>{task.missedRuns}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="muted">当前没有已采集的计划任务。</p>
        )}
        {!!tasks.length && (
          <div className="inventory-task-cards">
            {tasks.map((entry) => {
              const task = JSON.parse(entry.detail) as Task
              const states: Record<string, string> = {
                unknown: '未知',
                disabled: '已禁用',
                queued: '排队中',
                ready: '就绪',
                running: '运行中',
              }
              return (
                <article key={entry.scope + entry.id}>
                  <strong>{entry.name}</strong>
                  <p className="mono small muted">{entry.id}</p>
                  <dl>
                    <dt>状态</dt>
                    <dd>{!task.enabled ? '已禁用' : states[task.state] || task.state}</dd>
                    <dt>上次运行</dt>
                    <dd>{task.lastRun ? timestamp(task.lastRun) : '—'}</dd>
                    <dt>上次结果</dt>
                    <dd className="mono">{taskResult(task)}</dd>
                    <dt>下次运行</dt>
                    <dd>{task.nextRun ? timestamp(task.nextRun) : '未安排'}</dd>
                    <dt>遗漏次数</dt>
                    <dd>{task.missedRuns}</dd>
                  </dl>
                </article>
              )
            })}
          </div>
        )}
        <h3>启动登记项（{startup.length}）</h3>
        {startup.length ? (
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>名称</th>
                  <th>来源</th>
                  <th>观测时间</th>
                </tr>
              </thead>
              <tbody>
                {startup.map((entry) => (
                  <tr key={entry.scope + entry.id}>
                    <td>{entry.name}</td>
                    <td className="mono small">{entry.scope}</td>
                    <td>{timestamp(entry.ts)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="muted">当前没有已采集的启动登记项。</p>
        )}
      </Panel>
      <Panel
        title="最近启动项变化"
        className="inventory-panel"
        extra={<span className="count">{source.value?.changes.length || 0}</span>}
      >
        {source.value?.changes.length ? (
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>时间</th>
                  <th>变化</th>
                  <th>名称 / 来源</th>
                </tr>
              </thead>
              <tbody>
                {source.value.changes.map((change, index) => (
                  <tr key={index}>
                    <td>{timestamp(change.ts)}</td>
                    <td>
                      {{ added: '新增', changed: '修改', removed: '移除' }[change.change] || change.change}
                    </td>
                    <td>
                      {change.name}
                      <p className="mono small muted">{change.scope}</p>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="muted">保留期内没有已记录的变化。首次读取只建立基线。</p>
        )}
      </Panel>
    </div>
  )
}
