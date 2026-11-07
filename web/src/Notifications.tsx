import { useApp } from './state'
import { Empty, Icon, Panel } from './ui'
import { RuleTemplates, TemplateLibrary } from './TemplateLibrary'
export { FeishuSetup as Setup } from './FeishuSetup'

const kinds: Record<string, string> = {
  feishu_app: '飞书应用',
  feishu_webhook: '飞书自定义机器人',
  webhook: '通用 Webhook',
  toast: 'Windows 通知',
}
export function Notifications() {
  const { data } = useApp()
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
                      {channel.filter && Object.values(channel.filter).some((values) => values?.length)
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
      <TemplateLibrary />
      <RuleTemplates />
      <div className="empty-action">
        <a className="button" href="#/notifications/rules">
          管理规则通知渠道
        </a>
      </div>
    </>
  )
}
