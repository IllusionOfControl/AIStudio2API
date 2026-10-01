<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { api, type AdminSession } from '@/api'
import { useI18n } from '@/i18n'
import App from './App.vue'
import LoginPage from './components/LoginPage.vue'

const { t } = useI18n()
const session = ref<AdminSession | null>(null)
const error = ref('')

// setSession toggles between standalone login page and management console based on session.
function setSession(value: AdminSession): void {
  session.value = value
  history.replaceState(null, '', value.authenticated ? '/' : '/login')
}

// expireSession unmounts console and cleans up event subscriptions.
function expireSession(): void {
  setSession({ enabled: true, authenticated: false, username: '' })
}

// loadSession verifies session state before loading management data.
async function loadSession(): Promise<void> {
  error.value = ''
  try {
    setSession(await api.session())
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : t('common.error')
  }
}

// logout revokes the current session and redirects to login page.
async function logout(): Promise<void> {
  try {
    await api.logout()
    expireSession()
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : t('common.error')
  }
}

onMounted(() => {
  document.title = t('app.title')
  window.addEventListener('admin-session-expired', expireSession)
  void loadSession()
})
onUnmounted(() => window.removeEventListener('admin-session-expired', expireSession))
</script>

<template>
  <div
    v-if="error"
    role="alert"
    class="fixed top-4 right-4 z-50 max-w-sm rounded border border-red-500/40 bg-[#161b22] p-4 text-red-300"
  >
    {{ error }}
    <button type="button" class="ml-3 underline" @click="loadSession">
      {{ t('app.refresh') }}
    </button>
  </div>
  <App
    v-if="session?.authenticated"
    :admin-username="session.enabled ? session.username : ''"
    @logout="logout"
  />
  <LoginPage v-else-if="session" @authenticated="setSession" />
  <div v-else class="flex h-full items-center justify-center text-gray-400">
    {{ t('common.loading') }}
  </div>
</template>
