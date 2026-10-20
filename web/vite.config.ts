import { defineConfig, loadEnv, type ResolvedConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { validateInstallerBuildEnv } from './src/shared/installer/installerUrl.mjs'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), 'LABBIT_')
  const backendTarget = env.LABBIT_DEV_BACKEND_URL?.trim() || 'http://127.0.0.1:8080'

  return {
    plugins: [react(), {
      name: 'labbit-installer-url-guard',
      configResolved(config: ResolvedConfig) {
        // Use Vite's final env, including mode files and explicit root/envDir.
        validateInstallerBuildEnv(config.env.VITE_LABBIT_APPROVED_INSTALLER_URL ?? '')
      },
    }],
    server: {
      proxy: {
        '/api/v1': {
          target: backendTarget,
          changeOrigin: true,
        },
        '/realtime/v1': {
          target: backendTarget,
          changeOrigin: true,
          ws: true,
        },
      },
    },
  }
})
