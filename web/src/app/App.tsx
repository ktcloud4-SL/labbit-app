import { RouterProvider } from 'react-router-dom'

import { AppProviders } from './providers/AppProviders'
import { createAppRouter } from './router'

const appRouter = createAppRouter()

export function App() {
  return (
    <AppProviders>
      <RouterProvider router={appRouter} />
    </AppProviders>
  )
}
