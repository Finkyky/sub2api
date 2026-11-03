import { onBeforeUnmount, ref, watch } from 'vue'
import { useStepUp, StepUpCancelledError } from '@/composables/useStepUp'
// An epoch survives revoke/regrant; a late failure cannot open a fresh OTP prompt.
export function usePluginOperation(allowed: (permission: string) => boolean) {
  const stepUp = useStepUp(), busy = ref(false), action = ref('')
  let epoch = 0, alive = true
  function cancel() { epoch++; stepUp.onCancel() }
  watch(() => !action.value || allowed(action.value), valid => { if (!valid) cancel() }, { flush: 'sync' })
  async function execute<T>(permission: string, request: () => Promise<T>, current: () => boolean = () => true): Promise<T> {
    if (busy.value || !allowed(permission) || !alive) throw new StepUpCancelledError()
    const token = ++epoch; action.value = permission; busy.value = true
    const valid = () => alive && epoch === token && allowed(permission) && current()
    try {
      const result = await stepUp.run(async () => {
        if (!valid()) throw new StepUpCancelledError()
        try { return await request() } catch (e) { if (!valid()) throw new StepUpCancelledError(); throw e }
      })
      if (!valid()) throw new StepUpCancelledError()
      return result
    } finally { busy.value = false; action.value = '' }
  }
  onBeforeUnmount(() => { alive = false; cancel() })
  return { stepUp, busy, execute, cancel }
}
