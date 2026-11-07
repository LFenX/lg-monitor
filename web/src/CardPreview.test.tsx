import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it } from 'vitest'
import { CardPreview, safeLink } from './CardPreview'

it('renders columns, formatting and collapsible content without activating card actions or HTML', async () => {
  const user = userEvent.setup()
  const { container } = render(
    <CardPreview
      label="虚构卡片"
      card={{
        header: { template: 'carmine', title: { content: '虚构故障' } },
        body: {
          elements: [
            {
              tag: 'column_set',
              columns: [
                { tag: 'column', elements: [{ tag: 'markdown', content: '**主机**\n虚构主机' }] },
                { tag: 'column', elements: [{ tag: 'markdown', content: '目标：虚构服务' }] },
              ],
            },
            { tag: 'hr' },
            {
              tag: 'markdown',
              content:
                '<img src="https://example.com/fake" onerror="alert(1)">\n[危险链接](javascript:alert)\n<at id=all></at>',
            },
            {
              tag: 'button',
              text: { content: '测试按钮' },
              behaviors: [{ type: 'open_url', default_url: 'javascript:alert(1)' }],
            },
            {
              tag: 'collapsible_panel',
              header: { title: { content: '虚构日志' } },
              elements: [{ tag: 'markdown', content: '虚构日志内容' }],
            },
          ],
        },
      }}
    />,
  )
  expect(screen.getByText('主机')).toHaveProperty('tagName', 'STRONG')
  expect(container.querySelectorAll('.card-column')).toHaveLength(2)
  expect(container.querySelector('hr')).toBeInTheDocument()
  expect(container.querySelector('img, script, iframe, a')).toBeNull()
  expect(screen.getByRole('button', { name: '测试按钮' })).toBeDisabled()
  expect(screen.getByText(/@所有人/)).toBeVisible()
  const details = container.querySelector('details')!
  expect(details.open).toBe(false)
  await user.click(screen.getByText('虚构日志'))
  expect(details.open).toBe(true)
  expect(screen.getByText('虚构日志内容')).toBeVisible()
  expect(screen.getByText(/以飞书实际显示为准/)).toBeVisible()
})

it('accepts only HTTP links without credentials and bounds hostile nesting', () => {
  expect(safeLink('javascript:alert(1)')).toBeUndefined()
  expect(safeLink('https://fake:secret@example.com')).toBeUndefined()
  expect(safeLink('https://example.com/runbook')).toBe('https://example.com/runbook')
  let nested: unknown = { tag: 'markdown', content: '过深内容' }
  for (let i = 0; i < 25; i++) nested = { tag: 'column', elements: [nested] }
  render(<CardPreview label="层级边界" card={{ body: { elements: [nested] } }} />)
  expect(screen.getByText(/预览层级过深/)).toBeVisible()
  expect(screen.queryByText('过深内容')).toBeNull()
})

it('renders Markdown headings, lists, quotes and code blocks as text elements', () => {
  const { container } = render(
    <CardPreview
      label="虚构富文本"
      card={{
        body: {
          elements: [
            {
              tag: 'markdown',
              content:
                '# 自定义标题\n\n- 第一项\n- 第二项\n\n> 引用说明\n\n\x60\x60\x60text\n虚构日志\n\x60\x60\x60\n\n![虚构图](https://example.com/fake.png)',
            },
          ],
        },
      }}
    />,
  )
  expect(screen.getByRole('heading', { name: '自定义标题' })).toBeVisible()
  expect(container.querySelectorAll('li')).toHaveLength(2)
  expect(container.querySelector('blockquote')).toHaveTextContent('引用说明')
  expect(container.querySelector('pre code')).toHaveTextContent('虚构日志')
  expect(container.querySelector('img')).toBeNull()
  expect(screen.getByText('[图片：虚构图]')).toBeVisible()
})
