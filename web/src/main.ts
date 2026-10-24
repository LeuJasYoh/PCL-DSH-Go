import { createPinia } from 'pinia'
import { createApp } from 'vue'

import App from '@/App.vue'
import { setUnauthorizedHandler } from '@/api/client'
import router from '@/router'
import { useAuthStore } from '@/stores/auth'

import '@/styles/tokens.css'
import '@/styles/base.css'
import '@/styles/layout.css'
import '@/styles/components.css'

const pinia = createPinia()
const app = createApp(App)
app.use(pinia)
app.use(router)

// A 401 from any request (except the login call itself) means the session expired.
setUnauthorizedHandler(() => {
  const auth = useAuthStore(pinia)
  auth.clear()
  const current = router.currentRoute.value
  if (current.name === 'login') return
  const query = current.fullPath && current.fullPath !== '/' ? { redirect: current.fullPath } : {}
  void router.push({ name: 'login', query })
})

app.mount('#app')
