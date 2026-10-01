/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, test, vi } from 'vitest'

import { SignUpForm } from '../components/sign-up-form'

// Only the network and navigation boundaries are replaced: server status,
// the register request and the router.
const mocks = vi.hoisted(() => ({
  status: {} as Record<string, unknown>,
  register: vi.fn(),
}))

vi.mock('@/hooks/use-status', () => ({
  useStatus: () => ({ status: mocks.status, loading: false, error: null }),
}))

vi.mock('@tanstack/react-router', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@tanstack/react-router')>()),
  useNavigate: () => vi.fn(),
}))

vi.mock('@/features/auth/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/auth/api')>()),
  register: mocks.register,
}))

async function fillAccountFields(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText('Username'), 'new-user')
  await user.type(screen.getByLabelText('Password'), 'password-123')
  await user.type(screen.getByLabelText('Confirm password'), 'password-123')
}

afterEach(() => {
  mocks.status = {}
  window.history.replaceState({}, '', '/')
})

describe('sign-up invite code', () => {
  test('invite-only registration prefills the code from the invite link and shows the hint', () => {
    mocks.status = {
      invite_code_register_enabled: true,
      invite_code_register_hint: 'Join QQ group 593143573 to get a code',
    }
    window.history.replaceState({}, '', '/sign-up?invite_code=ABC123DEF456')

    render(<SignUpForm />)

    expect(screen.getByLabelText('Invite code')).toHaveValue('ABC123DEF456')
    expect(
      screen.getByText('Join QQ group 593143573 to get a code')
    ).toBeInTheDocument()
  })

  test('invite-only registration without a code shows a field error and sends nothing', async () => {
    mocks.status = { invite_code_register_enabled: true }
    const user = userEvent.setup()
    render(<SignUpForm />)

    await fillAccountFields(user)
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    expect(
      await screen.findByText('Please enter the invite code')
    ).toBeInTheDocument()
    expect(mocks.register).not.toHaveBeenCalled()
  })

  test('invite-only registration sends the trimmed invite code', async () => {
    mocks.status = { invite_code_register_enabled: true }
    mocks.register.mockResolvedValue({ success: true })
    const user = userEvent.setup()
    render(<SignUpForm />)

    await user.type(screen.getByLabelText('Invite code'), '  CODE12345678  ')
    await fillAccountFields(user)
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    await waitFor(() =>
      expect(mocks.register).toHaveBeenCalledWith(
        expect.objectContaining({
          username: 'new-user',
          invite_code: 'CODE12345678',
        })
      )
    )
  })

  test('invite-only registration hides third-party sign-up', () => {
    mocks.status = { invite_code_register_enabled: true, github_oauth: true }

    render(<SignUpForm />)

    expect(screen.queryByText('Continue with GitHub')).not.toBeInTheDocument()
  })

  test('open registration has no invite field, keeps third-party sign-up and sends no code', async () => {
    mocks.status = { github_oauth: true }
    mocks.register.mockResolvedValue({ success: true })
    window.history.replaceState({}, '', '/sign-up?invite_code=IGNOREDCODE1')
    const user = userEvent.setup()
    render(<SignUpForm />)

    expect(screen.queryByLabelText('Invite code')).not.toBeInTheDocument()
    expect(screen.getByText('Continue with GitHub')).toBeInTheDocument()

    await fillAccountFields(user)
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    await waitFor(() => expect(mocks.register).toHaveBeenCalledTimes(1))
    expect(mocks.register.mock.calls[0][0].invite_code).toBeUndefined()
  })
})
