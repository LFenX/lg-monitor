import { fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { expect, it, vi } from 'vitest'
import { Dialog } from './parts'

function Form({ onClose }: { onClose: () => void }) {
  const [name, setName] = useState(''),
    [note, setNote] = useState('')
  // A fresh close on every render, the way every caller writes it.
  return (
    <Dialog title="编辑服务" close={() => onClose()}>
      <input aria-label="名称" value={name} onChange={(event) => setName(event.target.value)} />
      <input aria-label="说明" value={note} onChange={(event) => setNote(event.target.value)} />
    </Dialog>
  )
}

it('focuses the first field once and leaves the caret where the person put it', () => {
  const onClose = vi.fn()
  render(<Form onClose={onClose} />)
  expect(document.activeElement).toBe(screen.getByLabelText('名称'))
  const note = screen.getByLabelText('说明')
  note.focus()
  fireEvent.change(note, { target: { value: '数据库' } })
  fireEvent.change(note, { target: { value: '数据库主库' } })
  expect(document.activeElement).toBe(note)
  fireEvent.keyDown(window, { key: 'Escape' })
  expect(onClose).toHaveBeenCalledTimes(1)
})
