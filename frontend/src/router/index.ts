import { createRouter, createWebHistory } from 'vue-router'
import ViewSwitcher from '@/views/ViewSwitcher.vue'

export default createRouter({
  history: createWebHistory(new URL(document.baseURI).pathname),
  routes: [
    { path: '/', component: ViewSwitcher },
    { path: '/logs', redirect: { path: '/', query: { view: 'logs' } } },
    { path: '/terminal', redirect: { path: '/', query: { view: __TERMINAL_ENABLED__ ? 'terminal' : 'overview' } } },
    { path: '/:pathMatch(.*)*', redirect: { path: '/', query: { view: 'workbench' } } },
  ],
})
