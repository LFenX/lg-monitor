export type TemplateDefinition = {
  id: string
  name?: string
  extends?: string
  title?: string
  colors?: Record<string, string>
  fields?: string[]
  extraFields?: { label: string; value: string }[]
  mention?: Record<string, 'all' | string[]>
  buttons?: { text: string; url: string }[]
  note?: string
  raw?: unknown
}
export type PreviewResult = { card: unknown; fallback: boolean; truncated: boolean; warning?: string }
export type ValidationIssue = { file: string; line: number; message: string }
export const states = [
  ['critical', '严重'],
  ['warning', '警告'],
  ['info', '提示'],
  ['resolved', '恢复'],
] as const
export const fields = [
  ['host', '主机'],
  ['target', '目标'],
  ['startedAt', '开始时间'],
  ['duration', '持续时长'],
  ['value', '当前值'],
] as const
export const colors = [
  ['blue', '蓝色'],
  ['wathet', '浅蓝'],
  ['turquoise', '青色'],
  ['green', '绿色'],
  ['yellow', '黄色'],
  ['orange', '橙色'],
  ['red', '红色'],
  ['carmine', '洋红'],
  ['violet', '紫罗兰'],
  ['purple', '紫色'],
  ['indigo', '靛蓝'],
  ['grey', '灰色'],
] as const
export const variables = [
  ['.Alert.Summary', '告警摘要'],
  ['.Alert.ID', '告警编号'],
  ['.Alert.State', '触发或恢复'],
  ['.Alert.StartedAt', '开始时间'],
  ['.Alert.ResolvedAt', '恢复时间'],
  ['.Alert.Duration', '持续时长'],
  ['.Rule.Name', '规则名称'],
  ['.Target.Name', '目标名称'],
  ['.Target.Tags', '目标标签列表'],
  ['.Host.Name', '主机名称'],
  ['.Value', '当前值'],
  ['.SeverityIcon', '级别图标'],
  ['.SeverityText', '级别文字'],
  ['.DashboardURL', '面板地址'],
  ['.Processes', '进程列表'],
  ['.Events', '故障前后事件，最多 5 条'],
  ['.LogTail', '日志末尾'],
  ['.Diagnosis', '诊断建议'],
  ['.Alerts', '汇总告警列表'],
]
export const initialRaw = JSON.stringify(
  {
    firing: {
      schema: '2.0',
      header: {
        template: 'carmine',
        title: { tag: 'plain_text', content: '{{.SeverityIcon}} {{.Rule.Name}}' },
      },
      body: { elements: [{ tag: 'markdown', content: '{{.Alert.Summary}}' }] },
    },
    resolved: {
      schema: '2.0',
      header: { template: 'green', title: { tag: 'plain_text', content: '已恢复：{{.Rule.Name}}' } },
      body: { elements: [{ tag: 'markdown', content: '{{.Target.Name}}，持续 {{.Alert.Duration}}' }] },
    },
  },
  null,
  2,
)

export function ruleTemplatePatch(rules: Record<string, unknown>[], id: string, template: string) {
  if (!rules.some((rule) => rule.id === id)) throw new Error('规则已不存在，请刷新后重试')
  return rules.map((rule) => (rule.id === id ? { ...rule, template: template || undefined } : rule))
}
