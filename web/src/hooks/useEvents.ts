import { useEffect, useRef } from 'react'
import { getToken } from '../api/client'

const TOPICS = [
  'hello',
  'volume.attached',
  'volume.detached',
  'job.started',
  'job.progress',
  'job.completed',
  'job.failed',
  'card.unknown',
  'card.decision',
  'dest.missing',
  'slot.calibrated',
]

/**
 * Subscribes to the SSE stream. Reconnects with backoff; the handler is kept
 * in a ref so re-renders never tear the connection down.
 */
export function useEvents(onEvent: (topic: string, data: unknown) => void) {
  const handler = useRef(onEvent)
  handler.current = onEvent

  useEffect(() => {
    let es: EventSource | null = null
    let closed = false
    let retryMs = 1000
    let timer: ReturnType<typeof setTimeout> | undefined

    const connect = () => {
      es = new EventSource(`/api/events?token=${encodeURIComponent(getToken())}`)
      for (const t of TOPICS) {
        es.addEventListener(t, (e) => {
          retryMs = 1000
          try {
            handler.current(t, JSON.parse((e as MessageEvent).data))
          } catch {
            /* payload não-JSON: ignora */
          }
        })
      }
      es.onerror = () => {
        es?.close()
        if (!closed) {
          timer = setTimeout(connect, retryMs)
          retryMs = Math.min(retryMs * 2, 15000)
        }
      }
    }
    connect()
    return () => {
      closed = true
      if (timer) clearTimeout(timer)
      es?.close()
    }
  }, [])
}
