import { watchEffect, shallowRef, type ObjectDirective } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { allows, type PermissionBinding } from '@/permissions/policy'

// Native elements only: use on the actual trigger or form, not fragment components.
// Capture guards also stop synthetic clicks and Enter submission while hidden.
const states = new WeakMap<HTMLElement, { value: ReturnType<typeof shallowRef<PermissionBinding>>; stop: () => void; guard: (e: Event) => void; display: string }>()
const events = ['click', 'dblclick', 'submit', 'change', 'input', 'keydown', 'pointerdown', 'contextmenu']
export const permissionDirective: ObjectDirective<HTMLElement, PermissionBinding> = {
  beforeMount(el, binding) {
    const auth = useAuthStore()
    const value = shallowRef(binding.value)
    const display = el.style.display
    const guard = (e: Event) => {
      if (!allows(auth, value.value)) { e.preventDefault(); e.stopImmediatePropagation() }
    }
    events.forEach(event => el.addEventListener(event, guard, true))
    const stop = watchEffect(() => {
      const permitted = allows(auth, value.value)
      el.style.setProperty('display', permitted ? display : 'none', permitted ? '' : 'important')
      el.hidden = !permitted
      el.inert = !permitted
      if (permitted) el.removeAttribute('aria-hidden')
      else el.setAttribute('aria-hidden', 'true')
    }, { flush: 'sync' })
    states.set(el, { value, stop, guard, display })
  },
  updated(el, binding) {
    const state = states.get(el)
    if (!state) return
    state.value.value = binding.value
    // v-show / style patching must not re-show a denied action.
    if (!allows(useAuthStore(), binding.value)) el.style.setProperty('display', 'none', 'important')
  },
  unmounted(el) {
    const state = states.get(el)
    state?.stop()
    events.forEach(event => state && el.removeEventListener(event, state.guard, true))
    states.delete(el)
  }
}
