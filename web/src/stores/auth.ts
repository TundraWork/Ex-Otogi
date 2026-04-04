import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

import { isApiError } from '@/api/client'
import { validateToken } from '@/api/management'

const AUTH_TOKEN_STORAGE_KEY = 'otogi.management.token'

export const useAuthStore = defineStore('auth', () => {
  const token = ref<string | null>(null)
  const isSubmitting = ref(false)
  const lastError = ref<string | null>(null)

  const isAuthenticated = computed(() => Boolean(token.value))

  function restoreSession() {
    const storedToken = localStorage.getItem(AUTH_TOKEN_STORAGE_KEY)?.trim()
    token.value = storedToken || null
  }

  function persistSession(nextToken: string | null) {
    token.value = nextToken

    if (nextToken) {
      localStorage.setItem(AUTH_TOKEN_STORAGE_KEY, nextToken)
      return
    }

    localStorage.removeItem(AUTH_TOKEN_STORAGE_KEY)
  }

  async function login(candidateToken: string) {
    const normalizedToken = candidateToken.trim()
    if (!normalizedToken) {
      lastError.value = 'Token is required.'
      return false
    }

    isSubmitting.value = true
    lastError.value = null

    try {
      await validateToken(normalizedToken)
      persistSession(normalizedToken)
      return true
    } catch (error) {
      if (isApiError(error)) {
        if (error.status === 401) {
          lastError.value = 'Token is invalid or expired.'
        } else {
          lastError.value = error.message
        }
      } else {
        lastError.value = 'Unable to validate token right now.'
      }

      return false
    } finally {
      isSubmitting.value = false
    }
  }

  function clearError() {
    lastError.value = null
  }

  function logout() {
    persistSession(null)
    clearError()
  }

  function handleUnauthorized() {
    logout()
  }

  return {
    clearError,
    handleUnauthorized,
    isAuthenticated,
    isSubmitting,
    lastError,
    login,
    logout,
    restoreSession,
    token,
  }
})
