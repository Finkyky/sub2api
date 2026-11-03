<template>
  <fieldset v-if="canRead && loaded && attributes.length > 0" :disabled="disabled || !canWrite" class="space-y-4">
    <div v-for="attr in attributes" :key="attr.id">
      <label class="input-label">
        {{ attr.name }}
        <span v-if="attr.required" class="text-red-500">*</span>
      </label>

      <!-- Text Input -->
      <input
        v-if="attr.type === 'text' || attr.type === 'email' || attr.type === 'url'"
        v-model="localValues[attr.id]"
        :type="attr.type === 'text' ? 'text' : attr.type"
        :required="attr.required"
        :placeholder="attr.placeholder"
        class="input"
        @input="emitChange"
      />

      <!-- Number Input -->
      <input
        v-else-if="attr.type === 'number'"
        :value="localValues[attr.id]"
        type="number"
        :required="attr.required"
        :placeholder="attr.placeholder"
        :min="attr.validation?.min"
        :max="attr.validation?.max"
        class="input"
        @input="localValues[attr.id] = ($event.target as HTMLInputElement).value; emitChange()"
      />

      <!-- Date Input -->
      <input
        v-else-if="attr.type === 'date'"
        v-model="localValues[attr.id]"
        type="date"
        :required="attr.required"
        class="input"
        @input="emitChange"
      />

      <!-- Textarea -->
      <textarea
        v-else-if="attr.type === 'textarea'"
        v-model="localValues[attr.id]"
        :required="attr.required"
        :placeholder="attr.placeholder"
        rows="3"
        class="input"
        @input="emitChange"
      />

      <!-- Select -->
      <Select
        v-else-if="attr.type === 'select'"
        v-model="localValues[attr.id]"
        :options="attr.options || []"
        :disabled="disabled || !canWrite"
        @change="emitChange"
      />

      <!-- Multi-Select (Checkboxes) -->
      <div v-else-if="attr.type === 'multi_select'" class="space-y-2">
        <label
          v-for="opt in attr.options"
          :key="opt.value"
          class="flex items-center gap-2"
        >
          <input
            type="checkbox"
            :value="opt.value"
            :checked="isOptionSelected(attr.id, opt.value)"
            @change="toggleMultiSelectOption(attr.id, opt.value)"
            class="h-4 w-4 rounded border-gray-300 text-primary-600"
          />
          <span class="text-sm text-gray-700 dark:text-gray-300">{{ opt.label }}</span>
        </label>
      </div>

      <!-- Description -->
      <p v-if="attr.description" class="input-hint">{{ attr.description }}</p>
    </div>
  </fieldset>

  <!-- Loading State -->
  <div v-else-if="loading" class="flex justify-center py-4">
    <svg class="h-5 w-5 animate-spin text-gray-400" fill="none" viewBox="0 0 24 24">
      <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
      <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
    </svg>
  </div>
  <div v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">
    {{ error }} <button type="button" class="btn btn-secondary" @click="load">{{ rt('rbac.retry') }}</button>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch, onBeforeUnmount } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { useI18n } from 'vue-i18n'
import { rbacMessages } from '@/components/admin/rbac/messages'
import { adminAPI } from '@/api/admin'
import type { UserAttributeDefinition, UserAttributeValuesMap } from '@/types'
import Select from '@/components/common/Select.vue'

interface Props {
  userId?: number
  disabled?: boolean
  modelValue: UserAttributeValuesMap
}

interface Emits {
  (e: 'update:modelValue', value: UserAttributeValuesMap): void
  (e: 'dirty'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const auth = useAuthStore()
const { t: rt } = useI18n({ useScope: 'local', messages: rbacMessages })
const canRead = computed(() => auth.hasPermission('user_attributes.read') && (!props.userId || auth.hasPermission('users.read')))
const canWrite = computed(() => canRead.value && auth.hasPermission('users.update'))
const loading = ref(false), loaded = ref(false), error = ref('')
const attributes = ref<UserAttributeDefinition[]>([])
const localValues = ref<UserAttributeValuesMap>({})
let generation = 0
async function load() {
  const seq = ++generation
  loaded.value = false; loading.value = false; error.value = ''; attributes.value = []; localValues.value = {}
  if (!canRead.value) return
  loading.value = true
  try {
    const [definitions, values] = await Promise.allSettled([
      adminAPI.userAttributes.listEnabledDefinitions(),
      props.userId ? adminAPI.userAttributes.getUserAttributeValues(props.userId) : Promise.resolve([])
    ])
    if (seq !== generation) return
    if (definitions.status === 'fulfilled') {
      attributes.value = definitions.value
      loaded.value = true
    }
    if (values.status === 'fulfilled') {
      localValues.value = Object.fromEntries(values.value.map(v => [v.attribute_id, v.value]))
      if (props.userId) emit('update:modelValue', { ...localValues.value })
    }
    if (definitions.status === 'rejected' || values.status === 'rejected') error.value = rt('rbac.loadFailed')
  } catch {
    if (seq === generation) error.value = rt('rbac.loadFailed')
  } finally { if (seq === generation) loading.value = false }
}

const emitChange = () => {
  if (!canWrite.value || !loaded.value || props.disabled) return
  emit('dirty')
  emit('update:modelValue', { ...localValues.value })
}

const isOptionSelected = (attrId: number, optionValue: string): boolean => {
  const value = localValues.value[attrId]
  if (!value) return false
  try {
    const arr = JSON.parse(value)
    return Array.isArray(arr) && arr.includes(optionValue)
  } catch {
    return false
  }
}

const toggleMultiSelectOption = (attrId: number, optionValue: string) => {
  if (!canWrite.value || !loaded.value || props.disabled) return
  let arr: string[] = []
  const value = localValues.value[attrId]
  if (value) {
    try {
      arr = JSON.parse(value)
      if (!Array.isArray(arr)) arr = []
    } catch {
      arr = []
    }
  }

  const index = arr.indexOf(optionValue)
  if (index > -1) {
    arr.splice(index, 1)
  } else {
    arr.push(optionValue)
  }

  localValues.value[attrId] = JSON.stringify(arr)
  emitChange()
}

watch(() => props.modelValue, (newVal) => {
  if (newVal && Object.keys(newVal).length > 0) {
    localValues.value = { ...newVal }
  }
}, { immediate: true })

watch(() => [props.userId, canRead.value], load, { immediate: true })
onBeforeUnmount(() => { generation++ })
</script>
