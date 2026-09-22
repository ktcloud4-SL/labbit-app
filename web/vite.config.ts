import { defineConfig, loadEnv } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const backendTarget = env.LABBIT_BACKEND_URL?.trim() || 'http://127.0.0.1:8080'

  return {
    plugins: [react()],
    server: {
      proxy: {
        '/api/v1': {
          target: backendTarget,
          changeOrigin: true,
        },
      },
    },
  }
})
