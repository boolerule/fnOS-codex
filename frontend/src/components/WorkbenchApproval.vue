<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Approval, ChatItem } from '@/serverapi/workbench'
import { useI18n } from '@/composables/useI18n'

const props = defineProps<{ approval: Approval; item?: ChatItem; busy: boolean }>()
const emit = defineEmits<{ decide: [id: string, decision: string, answers?: Record<string, { answers: string[] }>] }>()
const { t } = useI18n()
const answers = ref<Record<string, string>>({})
const choices = computed(() => {
  if (props.approval.method === 'item/permissions/requestApproval') return ['accept', 'decline']
  const allowed = props.approval.params.availableDecisions
  return ['accept','decline','cancel'].filter(d => !allowed?.length || allowed.includes(d))
})
function answer() {
  const result = Object.fromEntries((props.approval.params.questions || []).map(q => [q.id, { answers: [answers.value[q.id]] }]))
  emit('decide', props.approval.id, '', result)
}
</script>
<template>
  <section class="wb-approval" :aria-label="t('approval')">
    <h3>{{ t(approval.method.endsWith('/requestUserInput') ? 'answer_request' : 'approval') }}</h3>
    <p v-if="approval.params.reason">{{ approval.params.reason }}</p>
    <pre v-if="approval.params.command || item?.command">{{ approval.params.command || item?.command }}</pre>
    <pre v-if="approval.method === 'item/permissions/requestApproval'">{{ JSON.stringify(approval.params.permissions ?? approval.params, null, 2) }}</pre>
    <pre v-if="item?.changes">{{ JSON.stringify(item.changes, null, 2) }}</pre>
    <p v-if="approval.params.cwd" class="wb-meta">{{ approval.params.cwd }}</p>
    <details><summary>{{ t('details') }}</summary><pre>{{ JSON.stringify(approval.params, null, 2) }}</pre></details>
    <template v-if="approval.method.endsWith('/requestUserInput')">
      <label v-for="q in approval.params.questions" :key="q.id" class="block my-2">
        {{ q.question }}
        <input v-model="answers[q.id]" :type="q.isSecret ? 'password' : 'text'" :list="'options-' + approval.id + '-' + q.id" class="g-input w-full" autocomplete="off" />
        <datalist :id="'options-' + approval.id + '-' + q.id"><option v-for="o in q.options" :key="o.label" :value="o.label">{{ o.description }}</option></datalist>
      </label>
      <button class="g-btn-secondary" :disabled="busy || approval.params.questions?.some(q => !answers[q.id]?.trim())" @click="answer">{{ t('send_answer') }}</button>
    </template>
    <div v-else class="flex flex-wrap gap-2">
      <button v-for="d in choices" :key="d" :class="d === 'accept' ? 'g-btn-secondary' : 'g-btn-ghost'" :disabled="busy" @click="emit('decide', approval.id, d)">
        {{ t(d === 'accept' ? 'allow_once' : d === 'decline' ? 'deny' : 'cancel') }}
      </button>
    </div>
  </section>
</template>
