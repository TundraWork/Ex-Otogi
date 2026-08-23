import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig, type PluginOption } from 'vite'

export default defineConfig({
  plugins: [vue(), tailwindcss()] as PluginOption[],
  resolve: {
    alias: {
      '@': new URL('./src', import.meta.url).pathname,
    },
  },
  server: {
    port: 8382,
    strictPort: true,
    proxy: {
      '/panel': {
        target: 'http://localhost:8080',
      },
    },
  },
})
