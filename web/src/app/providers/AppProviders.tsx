import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useState, type PropsWithChildren } from 'react'

import { LabbitApiProvider } from '../../shared/api/LabbitApiProvider'
import type { LabbitApi } from '../../shared/api/labbitApi'

interface AppProvidersProps extends PropsWithChildren {
  api?: LabbitApi
}

function createQueryClient() {
  return new QueryClient()
}

export function AppProviders({ children, api }: AppProvidersProps) {
  const [queryClient] = useState(createQueryClient)

  return (
    <QueryClientProvider client={queryClient}>
      <LabbitApiProvider api={api}>{children}</LabbitApiProvider>
    </QueryClientProvider>
  )
}
