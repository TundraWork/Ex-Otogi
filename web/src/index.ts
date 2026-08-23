import './index.css'
import { definePreset } from '@primeuix/themes'
import Aura from '@primeuix/themes/aura'
import PrimeVue from 'primevue/config'
import { createApp } from 'vue'

import App from './App.vue'
import { configureApiClient } from './api/client'
import { pinia } from './app/pinia'
import { router } from './router'
import { useAuthStore } from './stores/auth'

const AppPreset = definePreset(Aura, {
  semantic: {
    primary: {
      50: '{zinc.50}',
      100: '{zinc.100}',
      200: '{zinc.200}',
      300: '{zinc.300}',
      400: '{zinc.400}',
      500: '{zinc.500}',
      600: '{zinc.600}',
      700: '{zinc.700}',
      800: '{zinc.800}',
      900: '{zinc.900}',
      950: '{zinc.950}',
    },
    colorScheme: {
      light: {
        surface: {
          0: '#ffffff',
          50: '#f8fafc',
          100: '#f1f5f9',
          200: '#e2e8f0',
          300: '#cbd5e1',
          400: '#94a3b8',
          500: '#64748b',
          600: '#475569',
          700: '#334155',
          800: '#1e293b',
          900: '#0f172a',
          950: '#020617',
        },
        primary: {
          color: '{zinc.950}',
          inverseColor: '#ffffff',
          hoverColor: '{zinc.900}',
          activeColor: '{zinc.800}',
        },
        highlight: {
          background: '{zinc.950}',
          focusBackground: '{zinc.700}',
          color: '#ffffff',
          focusColor: '#ffffff',
        },
      },
      dark: {
        primary: {
          color: '{zinc.50}',
          inverseColor: '{zinc.950}',
          hoverColor: '{zinc.100}',
          activeColor: '{zinc.200}',
        },
        highlight: {
          background: 'rgba(250, 250, 250, .16)',
          focusBackground: 'rgba(250, 250, 250, .24)',
          color: 'rgba(255,255,255,.87)',
          focusColor: 'rgba(255,255,255,.87)',
        },
      },
    },
  },
})

const app = createApp(App)
app.use(pinia)
app.use(PrimeVue, {
  ripple: true,
  theme: {
    preset: AppPreset,
    options: {
      cssLayer: {
        name: 'primevue',
        order:
          'properties, theme, keyframes, base, primevue, components, utilities',
      },
    },
  },
})
app.use(router)

const authStore = useAuthStore(pinia)
authStore.restoreSession()

configureApiClient({
  getAuthToken: () => authStore.token,
  onUnauthorized: async () => {
    authStore.handleUnauthorized()
    if (router.currentRoute.value.path !== '/login') {
      await router.replace('/login')
    }
  },
})

app.mount('#app')
