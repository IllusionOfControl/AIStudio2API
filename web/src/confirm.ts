import { reactive } from 'vue'

// confirmState holds the current confirmation dialog content and resolver
export const confirmState = reactive({
  open: false,
  message: '',
  action: '',
  resolve: null as ((confirmed: boolean) => void) | null,
})

// confirmAction opens a confirmation dialog and returns whether the user confirmed
export function confirmAction(message: string, action: string): Promise<boolean> {
  confirmState.resolve?.(false)
  return new Promise((resolve) => {
    Object.assign(confirmState, { open: true, message, action, resolve })
  })
}

// settleConfirm closes the confirmation dialog and returns the result
export function settleConfirm(confirmed: boolean): void {
  const resolve = confirmState.resolve
  Object.assign(confirmState, { open: false, resolve: null })
  resolve?.(confirmed)
}
