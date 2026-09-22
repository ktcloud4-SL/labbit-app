import { createContext, useContext, type PropsWithChildren } from 'react'

import { resolveLabbitApiMode } from './apiMode'
import { httpLabbitApi, type LabbitApi } from '../api/labbitApi'
import { mockLabbitApi } from '../api/mockLabbitApi'

const LabbitApiContext = createContext<LabbitApi | null>(null)

interface LabbitApiProviderProps extends PropsWithChildren {
  api?: LabbitApi
}

function defaultApi() {
  if (!import.meta.env.DEV) {
    return httpLabbitApi
  }

  const mode = resolveLabbitApiMode(true, import.meta.env.VITE_LABBIT_API_MODE)
  return mode === 'http' ? httpLabbitApi : mockLabbitApi
}

export function LabbitApiProvider({ children, api = defaultApi() }: LabbitApiProviderProps) {
  return <LabbitApiContext.Provider value={api}>{children}</LabbitApiContext.Provider>
}

export function useLabbitApi() {
  const api = useContext(LabbitApiContext)

  if (!api) {
    throw new Error('LabbitApiProvider가 필요합니다')
  }

  return api
}
