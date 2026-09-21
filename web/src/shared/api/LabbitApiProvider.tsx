import { createContext, useContext, type PropsWithChildren } from 'react'

import { httpLabbitApi, type LabbitApi } from '../api/labbitApi'
import { mockLabbitApi } from '../api/mockLabbitApi'

const LabbitApiContext = createContext<LabbitApi | null>(null)

interface LabbitApiProviderProps extends PropsWithChildren {
  api?: LabbitApi
}

function defaultApi() {
  return import.meta.env.DEV ? mockLabbitApi : httpLabbitApi
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
