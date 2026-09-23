import { useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'

import type {
  LoginLocationState,
  LoginReason,
} from '../routing/loginNavigation'

interface LoginRedirectProps {
  reason: LoginReason
}

export function LoginRedirect({ reason }: LoginRedirectProps) {
  const queryClient = useQueryClient()
  const location = useLocation()
  const navigate = useNavigate()
  const from = `${location.pathname}${location.search}`

  useEffect(() => {
    queryClient.clear()

    const state: LoginLocationState = {
      from,
      reason,
    }

    navigate('/login', { replace: true, state })
  }, [from, navigate, queryClient, reason])

  return null
}
