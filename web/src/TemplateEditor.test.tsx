import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { API } from './api'
import { TemplateEditor } from './TemplateEditor'

let api: API,
  role = 'config'
let bodies: { path: string; method: string; value: Record<string, unknown> }[]
vi.mock('./state', async (original) => ({
  ...(await original<typeof import('./state')>()),
  useApp: () => ({ api, scope: role, refresh: () => {} }),
}))
beforeEach(() => {
  role = 'config'
  bodies = []
  api = new API(
    'fake-config',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input),
        method = init?.method || 'GET'
      const value = init?.body ? JSON.parse(String(init.body)) : {}
      bodies.push({ path, method, value })
      if (method === 'GET')
        return Response.json({
          id: 'standard',
          name: '标准',
          extends: 'standard',
          fields: ['host', 'target'],
          title: '虚构初始标题',
        })
      const spec = (value.template || value) as Record<string, unknown>
      if (spec.title === '{{.Missing}}')
        return Response.json(
          { error: { message: 'executing at <.Missing>: map has no entry for key "Missing"' } },
          { status: 400 },
        )
      if (path.endsWith('/preview'))
        return Response.json({
          card: {
            header: { template: 'blue', title: { content: spec.title || '虚构高级卡片' } },
            body: { elements: [] },
          },
        })
      return Response.json(spec)
    }) as typeof fetch,
  )
})

it('previews field changes, validates with the backend before saving and rejects invalid raw JSON locally', async () => {
  const user = userEvent.setup()
  render(<TemplateEditor id="standard" />)
  const input = await screen.findByRole('textbox', { name: '标题' })
  await user.clear(input)
  await user.type(input, '虚构修改标题')
  const preview = await screen.findByRole('group', { name: '当前模板预览' })
  await waitFor(() => expect(within(preview).getByText('虚构修改标题')).toBeVisible())
  await user.type(screen.getByRole('textbox', { name: '模板代号' }), 'fake-custom')
  await waitFor(() => expect(screen.getByRole('button', { name: '校验并保存模板' })).toBeEnabled())
  await user.click(screen.getByRole('button', { name: '校验并保存模板' }))
  await screen.findByText('模板已校验并保存。')
  const write = bodies.findIndex((request) => request.method === 'PUT')
  expect(bodies[write - 1].path).toBe('/api/v1/templates/fake-custom/preview')
  expect(bodies[write].value.title).toBe('虚构修改标题')
  await user.click(screen.getByRole('button', { name: '高级模式' }))
  await user.clear(screen.getByRole('textbox', { name: '卡片 JSON' }))
  await user.paste('{')
  expect(await screen.findByText(/JSON 语法错误：/)).toBeVisible()
  expect(screen.getByRole('button', { name: '校验并保存模板' })).toBeDisabled()
})

it('marks the source field for a backend rendering error and keeps read access unable to save', async () => {
  const user = userEvent.setup()
  role = 'read'
  render(<TemplateEditor id="standard" />)
  const input = await screen.findByRole('textbox', { name: '标题' })
  await user.clear(input)
  await user.paste('{{.Missing}}')
  await waitFor(() => expect(input).toHaveAttribute('aria-invalid', 'true'))
  expect(screen.getByRole('button', { name: '校验并保存模板' })).toBeDisabled()
  expect(bodies.some((request) => request.method === 'PUT')).toBe(false)
})

it('keeps separators while typing several members and sends the complete member list to preview', async () => {
  const user = userEvent.setup()
  render(<TemplateEditor id="standard" />)
  const selector = await screen.findByRole('combobox', { name: '严重时' })
  await user.selectOptions(selector, 'users')
  const input = screen.getByRole('textbox', { name: '成员标识' })
  await user.type(input, 'ou_fake_one, ou_fake_two')
  expect(input).toHaveValue('ou_fake_one, ou_fake_two')
  await waitFor(() => {
    const last = bodies.filter((request) => request.path.endsWith('/preview')).at(-1)!
    const definition = (last.value.template || last.value) as { mention: { critical: string[] } }
    expect(definition.mention.critical).toEqual(['ou_fake_one', 'ou_fake_two'])
  })
})
