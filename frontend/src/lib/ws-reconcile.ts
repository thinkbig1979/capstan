/**
 * WebSocket reconciliation helper (audit finding P-6 / findings 17, 18).
 *
 * When a WS connection closes without a terminal/done frame the client cannot
 * know whether the backend operation succeeded, failed, or is still running.
 * Asserting failure is wrong (finding 17: backup live status lies on
 * disconnect). The correct action is to refetch from the source-of-truth query
 * and let the server state drive the UI.
 *
 * Usage (inside a WS onClose callback):
 *   reconcileOnClose({ completed: receivedDoneFrame, refetch: query.refetch })
 */
export interface ReconcileOnCloseArgs {
  /** True when a terminal/done frame was received before the socket closed. */
  completed: boolean
  /** Refetch the source-of-truth query to reconcile UI to server state. */
  refetch: () => void
}

/**
 * If the socket closed without a terminal frame, refetch to reconcile UI to
 * server truth instead of asserting failure.
 */
export function reconcileOnClose({ completed, refetch }: ReconcileOnCloseArgs): void {
  if (!completed) {
    refetch()
  }
}
