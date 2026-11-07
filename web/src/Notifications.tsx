import { useApp, useResource } from './state'
import { Empty, ErrorNotice, Icon, Panel } from './ui'
import type { Template } from './types'

const kinds: Record<string, string> = {
  feishu_app: '飞书应用',
  feishu_webhook: '飞书自定义机器人',
  webhook: '通用 Webhook',
  toast: 'Windows 通知',
}
export function Notifications() {
  const { api, data, version } = useApp(),
    templates = useResource((signal) => api.request<Template[]>('/templates', { signal }), [api, version])
  const apps = data?.notifiers.feishuApps || [],
    channels = data?.notifiers.notifiers || []
  return (
    <>
      <div className="page-heading">
        <div>
          <p className="eyebrow">NOTIFICATIONS</p>
          <h1>通知</h1>
          <p className="muted">查看告警去向、已接入的飞书应用和卡片模板。</p>
        </div>
        <a className="button primary" href="#/notifications/setup">
          <Icon name="bell" size={17} />
          接入飞书
        </a>
      </div>
      <ErrorNotice message={templates.error} />
      <Panel title="通知渠道" extra={<span className="count">{channels.length}</span>}>
        {channels.length ? (
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>渠道</th>
                  <th>类型</th>
                  <th>默认模板</th>
                  <th>筛选条件</th>
                </tr>
              </thead>
              <tbody>
                {channels.map((channel) => (
                  <tr key={channel.id}>
                    <td>
                      <strong>{channel.chatName || channel.id}</strong>
                      {channel.chatName && channel.chatName !== channel.id && (
                        <p className="small muted mono">{channel.id}</p>
                      )}
                    </td>
                    <td>{kinds[channel.type] || channel.type}</td>
                    <td>
                      {channel.type === 'webhook' || channel.type === 'toast'
                        ? '不使用卡片'
                        : channel.template || 'standard'}
                    </td>
                    <td className="small muted">
                      {channel.filter
                        ? Object.entries(channel.filter)
                            .map(
                              ([key, values]) =>
                                ((
                                  ({ severity: '级别', tags: '标签', collectors: '主机' }) as Record<
                                    string,
                                    string
                                  >
                                )[key] || key) +
                                '：' +
                                values?.join('、'),
                            )
                            .join('；')
                        : '全部接收'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <Empty title="还没有设置通知渠道">
            <a href="#/notifications/setup">查看飞书接入步骤</a>
          </Empty>
        )}
      </Panel>
      <Panel title="飞书应用">
        {apps.length ? (
          <div className="app-list">
            {apps.map((app) => (
              <article key={app.id}>
                <span className="host-icon">
                  <Icon name="bell" />
                </span>
                <div>
                  <strong>{app.id}</strong>
                  <p className="muted small">
                    {app.domain === 'lark' ? 'Lark' : '飞书'} · 凭据通过密钥库管理
                  </p>
                </div>
              </article>
            ))}
          </div>
        ) : (
          <Empty title="还没有接入飞书应用" />
        )}
      </Panel>
      <Panel title="卡片模板" extra={<span className="muted small">编辑与预览将在后续版本开放</span>}>
        {templates.value?.length ? (
          <div className="template-grid">
            {templates.value.map((template) => (
              <article key={template.id}>
                <span className="template-mark">
                  <Icon name="board" size={25} />
                </span>
                <strong>{template.name || template.id}</strong>
                <p className="muted small">{template.description || template.id}</p>
                <span className="soft-label small">{template.preset ? '预设模板' : '自定义模板'}</span>
              </article>
            ))}
          </div>
        ) : (
          <Empty title={templates.loading ? '正在读取模板' : '尚无卡片模板'} />
        )}
      </Panel>
    </>
  )
}

export function Setup() {
  return (
    <>
      <div className="breadcrumbs">
        <a href="#/notifications">通知</a>
        <span>/</span>
        <span>接入飞书</span>
      </div>
      <div className="page-heading">
        <div>
          <h1>接入飞书</h1>
          <p className="muted">使用应用身份发送告警，凭据加密保存在监控器电脑上。</p>
        </div>
      </div>
      <Panel title="使用命令行向导">
        <div className="guide">
          <p>网页开通向导将在后续版本开放。现在可以在监控器电脑的终端运行：</p>
          <pre>
            <code>lgmon feishu setup</code>
          </pre>
          <p>
            如果监控器使用了自定义数据目录，请为命令加上对应的 <code>--home</code> 参数。
          </p>
          <ol>
            <li>填写自己创建的飞书应用凭据。</li>
            <li>按向导检查机器人能力、开通权限并发布应用。</li>
            <li>把机器人加入自己的测试群，选择告警群和模板。</li>
            <li>发送测试卡片，亲自确认收到后保存配置。</li>
          </ol>
          <p className="muted">操作完成后返回通知页查看已保存的应用和渠道。</p>
          <a className="button primary" href="#/notifications">
            返回通知 <Icon name="arrow" size={17} />
          </a>
        </div>
      </Panel>
    </>
  )
}

export function Boards() {
  return (
    <>
      <div className="page-heading">
        <div>
          <p className="eyebrow">DASHBOARDS</p>
          <h1>面板</h1>
          <p className="muted">按业务组合主机、监控目标和指标。</p>
        </div>
      </div>
      <Panel title="声明式面板">
        <Empty title="声明式面板尚未开放">当前可以在总览和主机、目标详情中查看实时状态与历史曲线。</Empty>
        <div className="empty-action">
          <a href="#/" className="button">
            查看运行概览 <Icon name="arrow" size={16} />
          </a>
        </div>
      </Panel>
    </>
  )
}
